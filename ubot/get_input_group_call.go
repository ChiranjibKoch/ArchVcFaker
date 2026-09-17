package ubot

import (
	"fmt"

	tg "github.com/amarnathcjd/gogram/telegram"
)

func (ctx *Context) getInputGroupCall(chatId int64) (tg.InputGroupCall, error) {
	ctx.groupCallsMutex.RLock()
	call, ok := ctx.inputGroupCalls[chatId]
	ctx.groupCallsMutex.RUnlock()
	if ok {
		if call == nil {
			return nil, fmt.Errorf("group call for chatId %d is closed", chatId)
		}
		return call, nil
	}
	peer, err := ctx.app.ResolvePeer(chatId)
	if err != nil {
		return nil, err
	}
	switch chatPeer := peer.(type) {
	case *tg.InputPeerChannel:
		fullChat, err := ctx.app.ChannelsGetFullChannel(
			&tg.InputChannelObj{
				ChannelID:  chatPeer.ChannelID,
				AccessHash: chatPeer.AccessHash,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("ChannelsGetFullChannel failed: %w", err)
		}
		call = fullChat.FullChat.(*tg.ChannelFull).Call
		ctx.groupCallsMutex.Lock()
		ctx.inputGroupCalls[chatId] = call
		ctx.groupCallsMutex.Unlock()
	case *tg.InputPeerChat:
		fullChat, err := ctx.app.MessagesGetFullChat(chatPeer.ChatID)
		if err != nil {
			return nil, fmt.Errorf("MessagesGetFullChat failed: %w", err)
		}
		call = fullChat.FullChat.(*tg.ChatFullObj).Call
		ctx.groupCallsMutex.Lock()
		ctx.inputGroupCalls[chatId] = call
		ctx.groupCallsMutex.Unlock()
	default:
		return nil, fmt.Errorf("chatId %d is not a group call", chatId)
	}
	if call == nil {
		return nil, fmt.Errorf("group call for chatId %d is closed", chatId)
	}
	return call, nil
}
