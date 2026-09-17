package modules

import (
	"fmt"
	"strings"

	"tgmultibot/config"
	"tgmultibot/database"

	"github.com/amarnathcjd/gogram/telegram"
)

const (
	BtnAccess       = "🔑 Access"
	BtnGetToken     = "🎟 Get Access Token"
	BtnEnterToken   = "📥 Enter Access Token"
	BtnMyDelegates  = "👥 Who's Using My Token"
	BtnBackToAccess = "🔙 Back to Access"

	btnRequestAccessInline = "📨 Request Bot Access"
)

var accessPanelKB = telegram.NewKeyboard().AddRow(
	telegram.Button.Text(BtnGetToken),
).AddRow(
	telegram.Button.Text(BtnEnterToken),
).AddRow(
	telegram.Button.Text(BtnMyDelegates),
).AddRow(
	telegram.Button.Text(BtnBackToMenu),
).BuildReply(ropts)

func isAuthorizedOrOwner(userID int64) bool {
	if config.Owner != nil && userID == config.Owner.ID {
		return true
	}
	ok, err := database.IsAuthorized(userID)
	if err != nil {
		sendTraceback(fmt.Sprintf("isAuthorizedOrOwner: IsAuthorized(%d): %v", userID, err))
		return false
	}
	return ok
}

func isDelegateOnly(userID int64) bool {
	if config.Owner != nil && userID == config.Owner.ID {
		return false
	}
	authed, err := database.ListAuthorizedUsers()
	if err == nil && contains64(authed, userID) {
		return false
	}
	return database.IsDelegate(userID)
}

func contains64(s []int64, v int64) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func isUnauthorizedAccessRequestCallback(data string) bool {
	return strings.HasPrefix(data, "accreq:send:")
}

func sendUnauthorizedPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()
	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Data(btnRequestAccessInline, fmt.Sprintf("accreq:send:%d", userID)))

	_, _ = m.Reply(
		"🚫 <b>You are not authorized to use this bot.</b>\n\n"+
			"Tap the button below to send a request for approval to the bot owner.",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	)
	return telegram.ErrEndGroup
}
