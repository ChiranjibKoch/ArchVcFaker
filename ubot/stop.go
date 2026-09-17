package ubot

import "fmt"

func (ctx *Context) Stop(chatId any) error {
	parsedChatId, err := ctx.parseChatId(chatId)
	if err != nil {
		return err
	}
	ctx.presentations = stdRemove(ctx.presentations, parsedChatId)
	delete(ctx.callSources, parsedChatId)
	err = ctx.binding.Stop(parsedChatId)
	if err != nil {
		return err
	}
	ctx.groupCallsMutex.RLock()
	call := ctx.inputGroupCalls[parsedChatId]
	ctx.groupCallsMutex.RUnlock()
	if call != nil {
		_, err = ctx.app.PhoneLeaveGroupCall(call, 0)
		if err != nil {
			return fmt.Errorf("PhoneLeaveGroupCall failed: %w", err)
		}
	}
	ctx.groupCallsMutex.Lock()
	delete(ctx.inputGroupCalls, parsedChatId)
	ctx.groupCallsMutex.Unlock()
	return err
}
