# Multi-Language Internationalization System

This document describes the multi-language internationalization system implemented for ArchVcFight.

## Overview

The bot now supports three languages:
- 🇬🇧 **English (en)** - Default
- 🇧🇩 **Bengali (bn)** - বাংলা
- 🇮🇳 **Hindi (hi)** - हिंदी

## Architecture

### String Files
All strings are stored in YAML files in the `strings/` directory:
- `strings/en.yml` - English translations
- `strings/bn.yml` - Bengali translations  
- `strings/hi.yml` - Hindi translations

### Translation Function
The `t()` function from `strings/__init__.py` handles string retrieval:
```python
from strings import t

# Basic usage
text = t("not_authorized", lang)

# With placeholders
text = t("add_success", lang, chat_id)
```

### User Language Preference
User language preferences are stored in MongoDB:
```python
from ArchVcfight.database.mymongo import get_user_language, set_user_language

# Get user's language
lang = await get_user_language(user_id)

# Set user's language
await set_user_language(user_id, "bn")
```

## User Commands

### Language Selection
Users can change their language using:
- `/setlang` - Opens inline keyboard with language options
- Interactive menu in `/start` with Language button

## Implementation Details

### Plugin Files Updated
All bot plugin files now use the internationalization system:
- `ArchVcfight/plugins/bot/vc.py` - Voice chat commands
- `ArchVcfight/plugins/bot/bridge.py` - Bridge/call commands
- `ArchVcfight/plugins/bot/start.py` - Start/help commands
- `ArchVcfight/plugins/bot/admin.py` - Admin commands
- `ArchVcfight/plugins/bot/record.py` - Recording commands
- `ArchVcfight/plugins/bot/accounts.py` - Account management

### Pattern Used
Every command function follows this pattern:
```python
@deployment_guard
@Client.on_message(filters.command("example"))
async def cmd_example(client: Client, msg: Message):
    # Get user's preferred language
    lang = await get_user_language(msg.from_user.id)
    
    # Use t() for all user-facing messages
    await msg.reply(t("example_key", lang))
```

### Callback Queries
Callback query handlers also use the system:
```python
@cb_deployment_guard
@Client.on_callback_query(filters.regex(r"^example:"))
async def cb_example(client: Client, cq: CallbackQuery):
    lang = await get_user_language(cq.from_user.id)
    await cq.answer(t("example_response", lang))
```

## String Keys

All string keys are defined in the YAML files. Common categories include:

- **Common**: `not_authorized`, `admins_only`, `error`, etc.
- **VC Commands**: `play_usage`, `pause_success`, `level_set`, etc.
- **Bridge Commands**: `join_success`, `leave_success`, `audio_set`, etc.
- **Account Commands**: `addaccount_usage`, `listaccs_title`, etc.
- **Language**: `setlang_success`, `language_select`, `lang_english`, etc.

## Testing

Run the test script to verify the system:
```bash
python test_strings.py
```

## Adding New Strings

To add a new translatable string:

1. Add the key and translation to all three YAML files:
   ```yaml
   # strings/en.yml
   new_key: "Your English text here"
   
   # strings/bn.yml
   new_key: "আপনার বাংলা টেক্সট এখানে"
   
   # strings/hi.yml
   new_key: "यहाँ आपका हिंदी टेक्स्ट"
   ```

2. Use the key in your code:
   ```python
   await msg.reply(t("new_key", lang))
   ```

## Benefits

1. **User Experience**: Users can interact with the bot in their preferred language
2. **Maintainability**: All strings are centralized in YAML files
3. **Extensibility**: Easy to add new languages by creating new YAML files
4. **Consistency**: Ensures consistent terminology across the bot
5. **Testing**: Easy to test different language outputs

## Future Enhancements

Potential improvements:
- Add more languages (Tamil, Telugu, Malayalam, etc.)
- Implement language auto-detection based on user's Telegram language
- Add admin tools to manage translations
- Implement translation validation tools
