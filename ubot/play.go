package ubot

import (
	"fmt"
	"tgmultibot/ntgcalls"
)

func (ctx *Context) Play(chatId any, mediaDescription ntgcalls.MediaDescription) error {
	parsedChatId, err := ctx.parseChatId(chatId)
	if err != nil {
		return fmt.Errorf("parseChatId failed: %w", err)
	}
	if ctx.binding.Calls()[parsedChatId] != nil {
		return ctx.binding.SetStreamSources(parsedChatId, ntgcalls.CaptureStream, mediaDescription)
	}
	err = ctx.connectCall(parsedChatId, mediaDescription, "")
	if err != nil {
		return fmt.Errorf("connectCall failed: %w", err)
	}
	if parsedChatId < 0 {
		err = ctx.joinPresentation(parsedChatId, mediaDescription.Screen != nil)
		if err != nil {
			return fmt.Errorf("joinPresentation failed: %w", err)
		}
		err = ctx.updateSources(parsedChatId)
		if err != nil {
			return fmt.Errorf("updateSources failed: %w", err)
		}
	}
	return nil
}
