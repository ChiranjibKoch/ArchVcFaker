package modules

import (
	"context"
	"fmt"
	"runtime/debug"

	"tgmultibot/config"
	"tgmultibot/manager"

	"github.com/amarnathcjd/gogram/telegram"
)

var (
	c     *telegram.Client
	cm    *manager.ClientManager
	ropts = telegram.BuildReplyOptions{ResizeKeyboard: true}
)

const (
	BtnAddClient       = "➕ Add Client"
	BtnAddByPhone      = "📱 Add by Phone Number"
	BtnUploadSession   = "📤 Upload Session"
	BtnAddClientBack   = "🔙 Back"
	BtnAddClientCancel = "🚫 Cancel"
	BtnMyClients       = "📋 My Clients"
	BtnReact           = "❣️ Send Reaction"
	BtnJoinChannel     = "🔗 Join Channel"
	BtnJoinVoiceChat   = "🎙 Join Voice Chat"
	BtnLeaveVoiceChat  = "🔇 Leave Voice Chat"
	BtnPlayMedia       = "🎵 Player"
	BtnPanelList       = "📋 List Chats"
	BtnPanelAdd        = "➕ Add Chat"
	BtnAutoReact       = "🔁 Auto React"
	BtnAutoView        = "👁 Auto View"
	BtnBackToMenu      = "🔙 Back to Menu"
	BtnSwitchAccess    = "🔄 Switch Access"
)

const handlerGroup = 1

func Load(client *telegram.Client, mgr *manager.ClientManager) error {
	c = client
	cm = mgr

	cm.SetOnPlaybackEnd(func(ownerID int64, chatIDNum int64) {
		go notifyPlayerPlaybackEnd(ownerID, chatIDNum)
	})

	c.On(telegram.OnMessage, wrap(handleMediaCollector)).SetGroup(handlerGroup)

	c.On("message:/start", wrap(handleStart)).SetGroup(handlerGroup)
	c.On("message:/cancel", wrap(handleCancel)).SetGroup(handlerGroup)
	c.On("message:/help", ownerWrap(handleHelp)).SetGroup(handlerGroup)
	c.On("message:/approved", ownerWrap(handleApprovedUsers)).SetGroup(handlerGroup)
	c.On("message:/approve", ownerWrap(handleApproveUser)).SetGroup(handlerGroup)
	c.On("message:/disapprove", ownerWrap(handleDisapproveUser)).SetGroup(handlerGroup)
	c.On("message:"+BtnAddClient, wrap(handleAddClientPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnAddByPhone, wrap(handleAddByPhonePrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnUploadSession, wrap(handleUploadSessionPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnAddClientCancel, wrap(handleAddClientCancel)).SetGroup(handlerGroup)
	c.On("message:"+BtnAddClientBack, wrap(handleAddClientBack)).SetGroup(handlerGroup)
	c.On("message:"+BtnMyClients, wrap(handleMyClients)).SetGroup(handlerGroup)
	c.On("message:"+BtnReact, wrap(handleReactPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnJoinChannel, wrap(handleJoinChannelPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnJoinVoiceChat, wrap(handleJoinVoiceChatPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnLeaveVoiceChat, wrap(handleLeaveVoiceChat)).SetGroup(handlerGroup)
	c.On("message:"+BtnPlayMedia, wrap(handlePlayerPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnBackToMenu, wrap(handleStart)).SetGroup(handlerGroup)

	c.On("message:"+BtnAutoReact, wrap(handleAutoReactPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnAutoView, wrap(handleAutoViewPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnPanelList, wrap(handlePanelListPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnPanelAdd, wrap(handlePanelAddPrompt)).SetGroup(handlerGroup)
	c.On(telegram.OnCallback, wrapCb(handleAutoChatCallback)).SetGroup(handlerGroup)
	c.On(telegram.OnCallback, wrapCb(handleClientCallback)).SetGroup(handlerGroup)
	c.On(telegram.OnCallback, wrapCb(handlePlayerCallback)).SetGroup(handlerGroup)

	c.On("message:"+BtnAccess, wrap(handleAccessPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnGetToken, wrap(handleGetToken)).SetGroup(handlerGroup)
	c.On("message:"+BtnEnterToken, wrap(handleEnterTokenPrompt)).SetGroup(handlerGroup)
	c.On("message:"+BtnMyDelegates, wrap(handleMyDelegates)).SetGroup(handlerGroup)
	c.On("message:"+BtnBackToAccess, wrap(handleAccessPrompt)).SetGroup(handlerGroup)
	c.On(telegram.OnCallback, wrapCb(handleAccessCallback)).SetGroup(handlerGroup)

	c.On("message:"+BtnSwitchAccess, wrap(handleSwitchAccess)).SetGroup(handlerGroup)
	c.On(telegram.OnCallback, wrapCb(handleSwitchAccessCallback)).SetGroup(handlerGroup)

	return nil
}

func handleCancel(m *telegram.NewMessage) error {
	userID := m.SenderID()
	name := ps.current(userID)
	if name == "" {
		_, err := m.Reply("ℹ️ <b>No active process to cancel.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}
	ps.cancel(userID)
	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))
	_, err := m.Reply(
		fmt.Sprintf("🚫 <b>Cancelled:</b> <code>%s</code>\n\nTap <b>🔙 Back to Menu</b> to continue.", name),
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
	)
	return err
}

func guardProcess(m *telegram.NewMessage, name string) (context.Context, bool) {
	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Text(BtnBackToMenu))
	return guardProcessWithKeyboard(m, name, kb)
}

func guardProcessWithKeyboard(
	m *telegram.NewMessage,
	name string,
	kb *telegram.KeyboardBuilder,
) (context.Context, bool) {
	userID := m.SenderID()
	if existing := ps.current(userID); existing != "" {
		m.Reply(
			fmt.Sprintf(
				"⚙️ <b>Process already running:</b> <code>%s</code>\n\nSend /cancel to stop it first.",
				existing,
			),
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.BuildReply(ropts)},
		)
		return nil, false
	}
	ctx, ok := ps.tryStart(userID, name)
	return ctx, ok
}

func wrap(fn func(*telegram.NewMessage) error) func(*telegram.NewMessage) error {
	return func(m *telegram.NewMessage) (err error) {
		if !m.IsPrivate() {
			return nil
		}
		defer func() {
			if r := recover(); r != nil {
				trace := fmt.Sprintf("panic in handler: %v\n%s", r, debug.Stack())
				sendTraceback(trace)
				err = fmt.Errorf("panic: %v", r)
			}
		}()

		if !isAuthorizedOrOwner(m.SenderID()) {
			return sendUnauthorizedPrompt(m)
		}

		if err = fn(m); err != nil && err != telegram.ErrEndGroup {
			sendTraceback(fmt.Sprintf("error in handler: %v\n%s", err, debug.Stack()))
		}
		return
	}
}

func wrapCb(fn func(*telegram.CallbackQuery) error) func(*telegram.CallbackQuery) error {
	return func(cb *telegram.CallbackQuery) (err error) {
		if !cb.IsPrivate() {
			return nil
		}
		defer func() {
			if r := recover(); r != nil {
				trace := fmt.Sprintf("panic in callback: %v\n%s", r, debug.Stack())
				sendTraceback(trace)
				err = fmt.Errorf("panic: %v", r)
			}
		}()

		if !isUnauthorizedAccessRequestCallback(cb.DataString()) && !isAuthorizedOrOwner(cb.GetSenderID()) {
			_, err = cb.Answer("🚫 You are not authorized to use this bot.", &telegram.CallbackOptions{Alert: true})
			return
		}

		if err = fn(cb); err != nil {
			sendTraceback(fmt.Sprintf("error in callback: %v\n%s", err, debug.Stack()))
		}
		return
	}
}

func sendTraceback(text string) {
	if config.LoggerID != 0 {
		c.SendMessage(config.LoggerID,
			fmt.Sprintf("❌ <b>Error Traceback</b>\n\n<pre>%s</pre>", text),
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
	}
}
