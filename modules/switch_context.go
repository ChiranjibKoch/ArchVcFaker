package modules

import (
	"fmt"
	"sync"

	"tgmultibot/config"
	"tgmultibot/database"

	"github.com/amarnathcjd/gogram/telegram"
)

var (
	accessCtxMu sync.RWMutex
	accessCtx   = map[int64]int64{} // userID -> grantorID they're currently acting as (0/absent = personal)
)

func effectiveOwnerID(userID int64) int64 {
	accessCtxMu.RLock()
	id := accessCtx[userID]
	accessCtxMu.RUnlock()
	if id == 0 {
		return userID
	}
	return id
}

func setAccessCtx(userID, ownerID int64) {
	accessCtxMu.Lock()
	if ownerID == 0 || ownerID == userID {
		delete(accessCtx, userID)
	} else {
		accessCtx[userID] = ownerID
	}
	accessCtxMu.Unlock()
}

func isPersonalCtx(userID int64) bool {
	return effectiveOwnerID(userID) == userID
}

// clearDelegateCtx is called when a grantor revokes a delegate's access
// (terminate) or revokes their token. It resets any delegate currently
// acting as that grantor back to personal context.
func clearDelegateCtx(grantorID int64) {
	accessCtxMu.Lock()
	for userID, gid := range accessCtx {
		if gid == grantorID {
			delete(accessCtx, userID)
		}
	}
	accessCtxMu.Unlock()
}

// clearUserCtx resets a specific user back to personal (used when their
// delegation is terminated).
func clearUserCtx(userID int64) {
	accessCtxMu.Lock()
	delete(accessCtx, userID)
	accessCtxMu.Unlock()
}

// buildMenuKB builds the contextual reply keyboard for a user.
// Add Client is hidden in non-personal context.
func buildMenuKB(userID int64) telegram.ReplyMarkup {
	personal := isPersonalCtx(userID)
	isOwner := config.Owner != nil && userID == config.Owner.ID

	kb := telegram.NewKeyboard()
	if personal || isOwner {
		kb.AddRow(
			telegram.Button.Text(BtnAddClient),
			telegram.Button.Text(BtnMyClients),
		)
	} else {
		kb.AddRow(telegram.Button.Text(BtnMyClients))
	}
	kb.AddRow(
		telegram.Button.Text(BtnReact),
		telegram.Button.Text(BtnJoinChannel),
	).AddRow(
		telegram.Button.Text(BtnJoinVoiceChat),
		telegram.Button.Text(BtnLeaveVoiceChat),
	).AddRow(
		telegram.Button.Text(BtnPlayMedia),
	).AddRow(
		telegram.Button.Text(BtnAutoReact),
		telegram.Button.Text(BtnAutoView),
	).AddRow(
		telegram.Button.Text(BtnAccess),
		telegram.Button.Text(BtnSwitchAccess),
	)
	return kb.BuildReply(ropts)
}

// handleSwitchAccess sends the inline switch-access keyboard.
func handleSwitchAccess(m *telegram.NewMessage) error {
	userID := m.SenderID()
	text, kb := buildSwitchAccessMsg(userID)
	_, err := m.Reply(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
	return err
}

func buildSwitchAccessMsg(userID int64) (string, *telegram.KeyboardBuilder) {
	grantors := database.GrantorsFor(userID)
	current := effectiveOwnerID(userID)

	kb := telegram.NewKeyboard()

	personalLabel := "👤 Personal"
	if current == userID {
		personalLabel = "✅ " + personalLabel
	}
	kb.AddRow(telegram.Button.Data(personalLabel, "swacc:personal"))

	for _, grantorID := range grantors {
		label := grantorLabel(grantorID)
		if current == grantorID {
			label = "✅ " + label
		}
		kb.AddRow(telegram.Button.Data(label, fmt.Sprintf("swacc:use:%d", grantorID)))
	}

	currentLabel := "Personal"
	if current != userID {
		currentLabel = grantorLabel(current)
	}

	text := fmt.Sprintf(
		"🔄 <b>Switch Access Context</b>\n\n"+
			"Choose whose clients to act as.\n\n"+
			"• <b>Personal</b> — your own clients\n"+
			"• Other entries — delegated access via someone's token\n\n"+
			"<b>Active context:</b> %s",
		currentLabel,
	)
	return text, kb
}

func grantorLabel(grantorID int64) string {
	user, err := c.GetUser(grantorID)
	if err != nil || user == nil {
		return fmt.Sprintf("🔑 ID %d", grantorID)
	}
	if user.Username != "" {
		return "🔑 @" + user.Username
	}
	if user.FirstName != "" {
		return "🔑 " + user.FirstName
	}
	return fmt.Sprintf("🔑 ID %d", grantorID)
}

func handleSwitchAccessCallback(cb *telegram.CallbackQuery) error {
	data := cb.DataString()
	userID := cb.GetSenderID()

	switch {
	case data == "swacc:personal":
		setAccessCtx(userID, userID)
		return refreshSwitchMsg(cb, userID, "✅ Switched to Personal.")

	case len(data) > 10 && data[:10] == "swacc:use:":
		var grantorID int64
		if _, err := fmt.Sscanf(data[10:], "%d", &grantorID); err != nil || grantorID == 0 {
			_, err := cb.Answer("Invalid.")
			return err
		}
		// Verify delegation is still active.
		valid := false
		for _, g := range database.GrantorsFor(userID) {
			if g == grantorID {
				valid = true
				break
			}
		}
		if !valid {
			_, err := cb.Answer("This delegation is no longer active.", &telegram.CallbackOptions{Alert: true})
			return err
		}
		setAccessCtx(userID, grantorID)
		label := grantorLabel(grantorID)
		return refreshSwitchMsg(cb, userID, fmt.Sprintf("✅ Switched to %s.", label))
	}
	_, err := cb.Answer("")
	return err
}

func refreshSwitchMsg(cb *telegram.CallbackQuery, userID int64, toast string) error {
	text, kb := buildSwitchAccessMsg(userID)
	if _, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()}); err != nil {
		return err
	}
	// Push updated reply keyboard so Add Client visibility reflects new context.
	c.SendMessage(userID, "🏠 <b>Main Menu</b>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: buildMenuKB(userID)})
	_, err := cb.Answer(toast)
	return err
}

// noClientsMsg returns the appropriate "no clients" message depending on
// whether the user is in personal or delegated context.
func noClientsMsg(userID int64) string {
	ownerID := effectiveOwnerID(userID)
	if ownerID == userID {
		return "❌ <b>No clients found.</b>\n\nTap <b>➕ Add Client</b> to add one first."
	}
	return fmt.Sprintf("❌ <b>No clients found</b> for %s.", grantorLabel(ownerID))
}
