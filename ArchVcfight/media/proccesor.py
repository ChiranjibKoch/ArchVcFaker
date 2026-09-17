from __future__ import annotations
import math
import numpy as np
from scipy.signal import lfilter, resample_poly
from typing import Optional

_TG_SAMPLE_RATE   = 48000
_TG_FRAME_SAMPLES = 960
_TG_S16_MAX       = 32767
_TG_S16_MIN       = -32768
_OS               = 4

_AGC_ATK    = 0.9993
_AGC_REL    = 0.99995
_AGC_MIN    = 0.9
_AGC_MAX    = 12.0
_AGC_SMOOTH = 0.004

_COMP_KNEE   = 0.10
_COMP_ATK    = 0.9900
_COMP_REL    = 0.9997

_LIM_LA_MS   = 5.0
_LIM_ATK     = 0.9960
_LIM_REL     = 0.9999
_BRICKWALL   = 0.999

_EXC_DRIVE   = 2.8
_EXC_MIX     = 0.0

VOLUME_LEVELS = {
     1:    1.0,  2:    1.5,  3:    2.0,  4:    3.0,  5:    4.0,
     6:    5.0,  7:   10.0,  8:   15.0,  9:   20.0, 10:   30.0,
    11:   50.0, 12:  100.0, 13:  150.0, 14:  200.0, 15:  300.0,
    16:  400.0, 17:  500.0, 18:  750.0, 19: 1000.0, 20: 1500.0,
    21: 2000.0, 22: 2500.0, 23: 3000.0, 24: 4000.0, 25: 5000.0,
    26: 6000.0, 27: 7000.0, 28: 8000.0, 29: 9000.0, 30:10000.0,
    31:12000.0, 32:14000.0, 33:16000.0, 34:18000.0, 35:20000.0,
    36:25000.0, 37:30000.0, 38:35000.0, 39:40000.0, 40:50000.0,
    41: 60000.0, 42: 75000.0, 43: 90000.0, 44:110000.0, 45:130000.0,
    46:160000.0, 47:200000.0, 48:250000.0, 49:350000.0, 50:500000.0,
    51: 700000.0, 52:1000000.0, 53:1400000.0, 54:2000000.0,
    55:2800000.0, 56:4000000.0, 57:5600000.0, 58:8000000.0,
    59:11000000.0, 60:15000000.0,
}

BASS_LEVELS = {
     0: 0.0,  1: 0.1,  2: 0.2,  3: 0.3,  4: 0.4,
     5: 0.5,  6: 0.6,  7: 0.7,  8: 0.8,  9: 0.9,
    10: 1.0, 11: 1.1, 12: 1.2, 13: 1.3, 14: 1.4, 15: 1.5,
}

TREBLE_LEVELS = {
     0: 0.0,  1: 0.1,  2: 0.2,  3: 0.3,  4: 0.4,
     5: 0.5,  6: 0.6,  7: 0.7,  8: 0.8,  9: 0.9,
    10: 1.0, 11: 1.1, 12: 1.2, 13: 1.3, 14: 1.4, 15: 1.5,
}

_B_COMP_ATK  = np.array([1.0 - _COMP_ATK])
_A_COMP_ATK  = np.array([1.0, -_COMP_ATK])
_B_COMP_REL  = np.array([1.0 - _COMP_REL])
_A_COMP_REL  = np.array([1.0, -_COMP_REL])
_B_LIM_ATK   = np.array([1.0 - _LIM_ATK])
_A_LIM_ATK   = np.array([1.0, -_LIM_ATK])
_B_LIM_REL   = np.array([1.0 - _LIM_REL])
_A_LIM_REL   = np.array([1.0, -_LIM_REL])
_B_AGC_ATK   = np.array([1.0 - _AGC_ATK])
_A_AGC_ATK   = np.array([1.0, -_AGC_ATK])
_B_AGC_REL   = np.array([1.0 - _AGC_REL])
_A_AGC_REL   = np.array([1.0, -_AGC_REL])

def _env_follow(peaks: np.ndarray, zi_a: np.ndarray, zi_r: np.ndarray,
                b_a, a_a, b_r, a_r):
    env_a, new_a = lfilter(b_a, a_a, peaks, zi=zi_a)
    env_r, new_r = lfilter(b_r, a_r, peaks, zi=zi_r)
    return np.maximum(env_a, env_r), new_a, new_r

def _soft_knee_gr(env: np.ndarray, thr: float, ratio: float, knee: float) -> np.ndarray:
    gr = np.ones(len(env), dtype=np.float64)
    above = env > (thr + knee)
    in_k  = (~above) & (env > (thr - knee))
    ov = env[above]
    gr[above] = (thr + (ov - thr) / ratio) / np.maximum(ov, 1e-12)
    ik = env[in_k]
    t  = np.clip((ik - (thr - knee)) / (2.0 * knee), 0.0, 1.0)
    gr[in_k] = (1.0 - t) + t * (thr + (ik - thr) / ratio) / np.maximum(ik, 1e-12)
    return gr

def pitch_shift(x: np.ndarray, semitones: float) -> np.ndarray:
    if abs(semitones) < 0.01 or len(x) == 0:
        return x
    ratio = 2.0 ** (semitones / 12.0)
    n = len(x)
    target_len = int(n / ratio)
    if target_len < 1:
        target_len = 1
    from scipy.signal import resample
    y = resample(x, target_len)
    t_old = np.linspace(0, 1, len(y))
    t_new = np.linspace(0, 1, n)
    from scipy.interpolate import interp1d
    f = interp1d(t_old, y, kind='linear', fill_value='extrapolate')
    return f(t_new).astype(x.dtype)

class AudioProcessor:
    __slots__ = (
        "sample_rate", "_la",
        "_prev_bass_output",
        "_prev_treble_input", "_prev_treble_output",
        "_agc_gain",
        "_zi_agc_a",  "_zi_agc_r",
        "_zi_comp_a", "_zi_comp_r",
        "_zi_lim_a",  "_zi_lim_r",
        "_buf",
    )

    def __init__(self, sample_rate: int = _TG_SAMPLE_RATE,
                 lim_ms: float = _LIM_LA_MS) -> None:
        self.sample_rate = sample_rate
        self._la = max(1, int(sample_rate * lim_ms / 1000.0)) * _OS
        self._prev_bass_output = 0.0
        self._prev_treble_input = 0.0
        self._prev_treble_output = 0.0
        self._agc_gain   = 1.0
        self._zi_agc_a   = np.zeros(1, dtype=np.float64)
        self._zi_agc_r   = np.zeros(1, dtype=np.float64)
        self._zi_comp_a  = np.zeros(1, dtype=np.float64)
        self._zi_comp_r  = np.zeros(1, dtype=np.float64)
        self._zi_lim_a   = np.zeros(1, dtype=np.float64)
        self._zi_lim_r   = np.zeros(1, dtype=np.float64)
        self._buf = np.zeros(self._la, dtype=np.float64)

    def process(self, samples: np.ndarray, volume: int,
                bass: int = 15, treble: int = 15,
                low_pitch: int = 0, high_pitch: int = 0,
                brutal: bool = True) -> np.ndarray:
        if len(samples) == 0:
            return np.zeros(0, dtype=np.float64)
        x = samples.astype(np.float64)

        if volume == 1:
            return x / 32768.0

        if brutal:
            return self._brutal(x, volume, bass, treble, low_pitch, high_pitch)
        return self._normal(x, volume)

    def _brutal(self, x, volume, bass, treble, low_pitch, high_pitch):
        if bass > 0:
            old_bass = min(15, bass // 2)
            blend = BASS_LEVELS.get(old_bass, 0.0)
            if blend > 0.0:
                lp_alpha = 0.85
                lp_filtered = np.zeros_like(x)
                prev_lp = self._prev_bass_output
                for i in range(len(x)):
                    lp_filtered[i] = lp_alpha * prev_lp + (1.0 - lp_alpha) * x[i]
                    prev_lp = lp_filtered[i]
                self._prev_bass_output = prev_lp
                x = x + blend * lp_filtered

        if treble > 0:
            old_treble = min(15, treble // 2)
            blend = TREBLE_LEVELS.get(old_treble, 0.0)
            if blend > 0.0:
                hp_alpha = 0.9
                hp_filtered = np.zeros_like(x)
                prev_in, prev_out = self._prev_treble_input, self._prev_treble_output
                for i in range(len(x)):
                    curr = x[i]
                    hp_filtered[i] = hp_alpha * (prev_out + curr - prev_in)
                    prev_in, prev_out = curr, hp_filtered[i]
                self._prev_treble_input, self._prev_treble_output = prev_in, prev_out
                x = x + blend * hp_filtered

        net_sem = float(high_pitch - low_pitch)
        if abs(net_sem) >= 0.01:
            x = pitch_shift(x, net_sem)

        gain = VOLUME_LEVELS.get(volume, 1.0)
        x = np.tanh(x * gain)

        x = self._agc(x, volume)
        x = self._compress(x, volume)
        x = self._limit(x, volume)

        return np.clip(np.nan_to_num(x, nan=0.0, posinf=1.0, neginf=-1.0),
                       -_BRICKWALL, _BRICKWALL)

    def _normal(self, x, volume):
        x *= max(0.0, volume) / 100.0
        pk = np.max(np.abs(x))
        if pk > 0.95:
            x *= 0.95 / pk
        return np.clip(np.nan_to_num(x, nan=0.0, posinf=1.0, neginf=-1.0), -1.0, 1.0)

    def _agc(self, x: np.ndarray, level: int) -> np.ndarray:
        target = 0.30 + 0.50 * ((level - 1) / 59)
        peaks = np.abs(x)
        env, self._zi_agc_a, self._zi_agc_r = _env_follow(
            peaks, self._zi_agc_a, self._zi_agc_r,
            _B_AGC_ATK, _A_AGC_ATK, _B_AGC_REL, _A_AGC_REL,
        )
        rms_env = float(np.sqrt(np.mean(env ** 2) + 1e-12))
        target_gain = float(np.clip(target / rms_env, _AGC_MIN, _AGC_MAX))
        self._agc_gain = (1.0 - _AGC_SMOOTH) * self._agc_gain + _AGC_SMOOTH * target_gain
        return x * self._agc_gain

    def _compress(self, x: np.ndarray, level: int) -> np.ndarray:
        thr = 0.35 - 0.25 * ((level - 1) / 59)
        ratio = 2.0 + 6.0 * ((level - 1) / 59)
        x_up = resample_poly(x, _OS, 1).astype(np.float64)
        env, self._zi_comp_a, self._zi_comp_r = _env_follow(
            np.abs(x_up), self._zi_comp_a, self._zi_comp_r,
            _B_COMP_ATK, _A_COMP_ATK, _B_COMP_REL, _A_COMP_REL,
        )
        x_up *= _soft_knee_gr(env, thr, ratio, _COMP_KNEE)
        out = resample_poly(x_up, 1, _OS)
        n = len(x)
        return out[:n] if len(out) >= n else np.pad(out, (0, n - len(out)))

    def _limit(self, x: np.ndarray, level: int) -> np.ndarray:
        thr = 0.92 + 0.07 * ((level - 1) / 59)
        x_up = resample_poly(x, _OS, 1).astype(np.float64)
        la = self._la
        padded = np.concatenate([self._buf, x_up])
        d = padded[la:la + len(x_up)]
        self._buf = (padded[-la:] if len(padded) >= la
                     else np.pad(padded, (la - len(padded), 0)))
        env, self._zi_lim_a, self._zi_lim_r = _env_follow(
            np.abs(x_up), self._zi_lim_a, self._zi_lim_r,
            _B_LIM_ATK, _A_LIM_ATK, _B_LIM_REL, _A_LIM_REL,
        )
        gr = np.minimum(1.0, thr / np.maximum(env, 1e-12))
        limited = d * gr
        out = resample_poly(limited, 1, _OS)
        n = len(x)
        return out[:n] if len(out) >= n else np.pad(out, (0, n - len(out)))

_global_processor: Optional[AudioProcessor] = None

def init_processor(sample_rate: int = _TG_SAMPLE_RATE,
                   lim_ms: float = _LIM_LA_MS) -> None:
    global _global_processor
    if _global_processor is None:
        _global_processor = AudioProcessor(sample_rate, lim_ms)

def _get() -> AudioProcessor:
    global _global_processor
    if _global_processor is None:
        init_processor()
    return _global_processor

def process_audio(samples: np.ndarray, volume: int, bass: int = 15,
                  treble: int = 15, low_pitch: int = 0, high_pitch: int = 0,
                  brutal: bool = True) -> np.ndarray:
    return _get().process(samples, volume, bass, treble, low_pitch, high_pitch, brutal)

def to_telegram_pcm(float_samples: np.ndarray, pad_silence: bool = True) -> bytes:
    x = np.nan_to_num(float_samples, nan=0.0, posinf=1.0, neginf=-1.0)
    x = np.clip(x * (_TG_S16_MAX * 0.9998), _TG_S16_MIN + 1, _TG_S16_MAX)
    s16 = x.astype(np.int16)
    if pad_silence:
        rem = len(s16) % _TG_FRAME_SAMPLES
        if rem:
            s16 = np.concatenate([s16, np.zeros(_TG_FRAME_SAMPLES - rem, dtype=np.int16)])
    return s16.tobytes()

def chunk_for_telegram(float_samples: np.ndarray, frame_count: int = 1) -> list[bytes]:
    spc = _TG_FRAME_SAMPLES * frame_count
    x = np.nan_to_num(float_samples, nan=0.0, posinf=1.0, neginf=-1.0)
    x = np.clip(x * (_TG_S16_MAX * 0.9998), _TG_S16_MIN + 1, _TG_S16_MAX)
    s16 = x.astype(np.int16)
    rem = len(s16) % spc
    if rem:
        s16 = np.concatenate([s16, np.zeros(spc - rem, dtype=np.int16)])
    return [s16[i:i + spc].tobytes() for i in range(0, len(s16), spc)]

def maximize_loudness(samples: np.ndarray, volume: int = 100, bass: int = 15,
                      treble: int = 15, low_pitch: int = 0,
                      high_pitch: int = 0) -> bytes:
    if volume == 0:
        s16 = np.clip(samples, _TG_S16_MIN + 1, _TG_S16_MAX).astype(np.int16)
        rem = len(s16) % _TG_FRAME_SAMPLES
        if rem:
            s16 = np.concatenate([s16, np.zeros(_TG_FRAME_SAMPLES - rem, dtype=np.int16)])
        return s16.tobytes()
    out = _get().process(samples, volume, bass, treble, low_pitch, high_pitch, brutal=True)
    return to_telegram_pcm(out, pad_silence=True)
