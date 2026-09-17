import os
import yaml
import logging
from typing import Any

logger = logging.getLogger(__name__)

_STRINGS: dict[str, dict[str, str]] = {}
_DEFAULT_LANG = "en"

def _load_strings():
    global _STRINGS
    strings_dir = os.path.dirname(os.path.abspath(__file__))
    for filename in os.listdir(strings_dir):
        if filename.endswith('.yml') or filename.endswith('.yaml'):
            lang_code = filename.split('.')[0]
            filepath = os.path.join(strings_dir, filename)
            try:
                with open(filepath, 'r', encoding='utf-8') as f:
                    _STRINGS[lang_code] = yaml.safe_load(f) or {}
                logger.info(f"Loaded {len(_STRINGS[lang_code])} strings for {lang_code}")
            except Exception as e:
                logger.error(f"Failed to load {filename}: {e}")

def t(key: str, lang: str = None, *args) -> str:
    if not _STRINGS:
        _load_strings()
    
    lang = lang or _DEFAULT_LANG
    if lang not in _STRINGS:
        lang = _DEFAULT_LANG
    
    strings = _STRINGS.get(lang, {})
    text = strings.get(key, key)
    
    if args:
        try:
            return text.format(*args)
        except (IndexError, KeyError):
            return text
    return text

_load_strings()
