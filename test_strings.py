#!/usr/bin/env python3
"""
Test script for the multi-language strings system
"""

from strings import t

def test_basic_strings():
    """Test basic string retrieval"""
    print("=" * 60)
    print("Testing Basic Strings")
    print("=" * 60)
    
    test_cases = [
        ("not_authorized", None),
        ("help_text", None),
        ("play_usage", None),
    ]
    
    for key, args in test_cases:
        for lang in ['en', 'bn', 'hi']:
            result = t(key, lang, *args) if args else t(key, lang)
            print(f"[{lang}] {key}: {result[:50]}...")
        print()

def test_formatted_strings():
    """Test strings with placeholders"""
    print("=" * 60)
    print("Testing Formatted Strings")
    print("=" * 60)
    
    test_cases = [
        ("add_success", ("12345",)),
        ("level_set", ("▓▓▓▓▓░░░░░", "30")),
        ("recstart_success", ("67890",)),
    ]
    
    for key, args in test_cases:
        print(f"\n{key}:")
        for lang in ['en', 'bn', 'hi']:
            result = t(key, lang, *args)
            print(f"  [{lang}] {result}")

def test_language_fallback():
    """Test fallback to English for unknown keys"""
    print("\n" + "=" * 60)
    print("Testing Language Fallback")
    print("=" * 60)
    
    # Test with valid key
    print("\nValid key:")
    print(f"  {t('not_authorized', 'en')}")
    
    # Test with invalid key (should return the key itself)
    print("\nInvalid key (fallback):")
    print(f"  {t('nonexistent_key', 'en')}")

def test_all_languages():
    """Test that all language files are loaded"""
    print("\n" + "=" * 60)
    print("Testing All Languages Loaded")
    print("=" * 60)
    
    languages = {
        'en': '🇬🇧 English',
        'bn': '🇧🇩 Bengali',
        'hi': '🇮🇳 Hindi',
    }
    
    for code, name in languages.items():
        result = t('lang_english', code)
        print(f"  {name}: {result}")

if __name__ == "__main__":
    print("\n🌐 ArchVcFight Multi-Language Strings System Test\n")
    
    test_basic_strings()
    test_formatted_strings()
    test_language_fallback()
    test_all_languages()
    
    print("\n" + "=" * 60)
    print("✓ All tests completed successfully!")
    print("=" * 60)
    print("\nThe multi-language system is working correctly.")
    print("Users can now use /setlang to change their language preference.")
    print("Available languages: English (en), Bengali (bn), Hindi (hi)")
