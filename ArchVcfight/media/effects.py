# effects.py
from __future__ import annotations
import numpy as np
from scipy.signal import sosfilt, sosfilt_zi

class BiquadFilter:
    __slots__ = ("_sos", "_zi")
    def __init__(self) -> None:
        self._sos: np.ndarray | None = None
        self._zi:  np.ndarray | None = None
    def low_shelf(self, x: np.ndarray, gain_db: float,
                  sample_rate: int = 48_000, f0: float = 200.0) -> np.ndarray:
        return self._apply(_low_shelf_sos(gain_db, f0, sample_rate), x)
    def high_shelf(self, x: np.ndarray, gain_db: float,
                   sample_rate: int = 48_000, f0: float = 4_000.0) -> np.ndarray:
        return self._apply(_high_shelf_sos(gain_db, f0, sample_rate), x)
    def peaking(self, x: np.ndarray, gain_db: float, f0: float,
                q: float, sample_rate: int = 48_000) -> np.ndarray:
        return self._apply(_peaking_sos(gain_db, f0, q, sample_rate), x)
    def reset(self) -> None:
        self._zi = None
    def _apply(self, sos: np.ndarray, x: np.ndarray) -> np.ndarray:
        if len(x) == 0:
            return x.copy()
        if self._sos is None or not np.array_equal(self._sos, sos):
            self._sos = sos
            self._zi  = sosfilt_zi(sos) * x[0]
        y, self._zi = sosfilt(sos, x, zi=self._zi)
        return y

class HarmonicExciter:
    __slots__ = ()
    def process(self, x: np.ndarray, drive: float = 2.5, mix: float = 0.14,
                sample_rate: int = 48_000) -> np.ndarray:
        if len(x) == 0 or mix <= 0.0:
            return x.copy()
        saturated = np.tanh(x * drive) / np.tanh(np.float64(drive))
        return x * (1.0 - mix) + saturated * mix

def _shelf_alpha(w0: float, A: float, S: float = 1.0) -> float:
    return float(np.sin(w0) / 2.0 * np.sqrt((A + 1.0 / A) * (1.0 / S - 1.0) + 2.0))

def _low_shelf_sos(gain_db: float, f0: float, fs: int) -> np.ndarray:
    A      = 10.0 ** (gain_db / 40.0)
    w0     = 2.0 * np.pi * f0 / fs
    cw     = np.cos(w0)
    alpha  = _shelf_alpha(w0, A)
    sqA    = np.sqrt(A)
    b0 =  A * ((A+1) - (A-1)*cw + 2*sqA*alpha)
    b1 =  2*A * ((A-1) - (A+1)*cw)
    b2 =  A * ((A+1) - (A-1)*cw - 2*sqA*alpha)
    a0 =      (A+1) + (A-1)*cw + 2*sqA*alpha
    a1 = -2 * ((A-1) + (A+1)*cw)
    a2 =      (A+1) + (A-1)*cw - 2*sqA*alpha
    return np.array([[b0/a0, b1/a0, b2/a0, 1.0, a1/a0, a2/a0]])

def _high_shelf_sos(gain_db: float, f0: float, fs: int) -> np.ndarray:
    A      = 10.0 ** (gain_db / 40.0)
    w0     = 2.0 * np.pi * f0 / fs
    cw     = np.cos(w0)
    alpha  = _shelf_alpha(w0, A)
    sqA    = np.sqrt(A)
    b0 =  A * ((A+1) + (A-1)*cw + 2*sqA*alpha)
    b1 = -2*A * ((A-1) + (A+1)*cw)
    b2 =  A * ((A+1) + (A-1)*cw - 2*sqA*alpha)
    a0 =      (A+1) - (A-1)*cw + 2*sqA*alpha
    a1 =  2 * ((A-1) - (A+1)*cw)
    a2 =      (A+1) - (A-1)*cw - 2*sqA*alpha
    return np.array([[b0/a0, b1/a0, b2/a0, 1.0, a1/a0, a2/a0]])

def _peaking_sos(gain_db: float, f0: float, q: float, fs: int) -> np.ndarray:
    A      = 10.0 ** (gain_db / 40.0)
    w0     = 2.0 * np.pi * f0 / fs
    alpha  = np.sin(w0) / (2.0 * q)
    cw     = np.cos(w0)
    b0 =  1 + alpha * A
    b1 = -2 * cw
    b2 =  1 - alpha * A
    a0 =  1 + alpha / A
    a1 = -2 * cw
    a2 =  1 - alpha / A
    return np.array([[b0/a0, b1/a0, b2/a0, 1.0, a1/a0, a2/a0]])

def pitch_shift(samples: np.ndarray, semitones: float) -> np.ndarray:
    if abs(semitones) < 0.01 or len(samples) == 0:
        return samples
    rate  = 2.0 ** (semitones / 12.0)
    n_in  = len(samples)
    n_rs  = max(1, int(round(n_in / rate)))
    src   = np.linspace(0.0, float(n_in - 1), n_rs)
    out   = np.interp(src, np.arange(n_in, dtype=np.float64), samples.astype(np.float64))
    fl    = min(256, n_rs // 8)
    if fl > 0:
        fade = np.cos(np.linspace(0.0, np.pi / 2.0, fl)) ** 2
        out[:fl]  *= fade
        out[-fl:] *= fade[::-1]
    if n_rs < n_in:
        result = np.zeros(n_in, dtype=np.float64)
        result[:n_rs] = out
        return result
    return out[:n_in]
