package modules

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"tgmultibot/config"
	"tgmultibot/database"

	"github.com/amarnathcjd/gogram/telegram"
)

var accessTokenFormatRE = regexp.MustCompile(`^[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}-[A-Z0-9]{4}$`)

func normalizeAccessToken(raw string) (string, bool) {
	token := strings.ToUpper(strings.TrimSpace(raw))
	return token, accessTokenFormatRE.MatchString(token)
}

var accessBackKB = telegram.NewKeyboard().AddRow(
	telegram.Button.Text(BtnBackToAccess),
).BuildReply(ropts)

type pendingAccessRequest struct {
	kind string
}

var (
	pendingAccessMu       sync.Mutex
	pendingAccessRequests = map[int64]pendingAccessRequest{}
)

func rememberPendingAccessRequest(userID int64, pending pendingAccessRequest) bool {
	pendingAccessMu.Lock()
	defer pendingAccessMu.Unlock()
	if _, exists := pendingAccessRequests[userID]; exists {
		return false
	}
	pendingAccessRequests[userID] = pending
	return true
}

func clearPendingAccessRequest(userID int64) {
	pendingAccessMu.Lock()
	delete(pendingAccessRequests, userID)
	pendingAccessMu.Unlock()
}

// ---- Access panel ------------------------------------------------------------

func handleAccessPrompt(m *telegram.NewMessage) error {
	_, err := m.Reply(
		"<b>🔑 Access Panel</b>\n\n"+
			"🎟 <b>Get Access Token</b> — view your permanent token to share with others\n"+
			"📥 <b>Enter Access Token</b> — claim access using someone else's token\n"+
			"👥 <b>Who's Using My Token</b> — see/terminate people using your accounts",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB},
	)
	return err
}

func handleGetToken(m *telegram.NewMessage) error {
	userID := m.SenderID()

	token, err := database.GetOrCreateAccessToken(userID)
	if err != nil {
		sendTraceback(fmt.Sprintf("GetOrCreateAccessToken for user %d: %v", userID, err))
		_, err := m.Reply("❌ <b>Failed to fetch token, please try again.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB})
		return err
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Data("🔄 Regenerate Token", "actok:regenask"))

	_, err = m.Reply(
		fmt.Sprintf(
			"🎟 <b>Your Access Token</b>\n\n"+
				"<code>%s</code>\n\n"+
				"Share this with anyone you want to give access to. They can claim it via "+
				"<b>🔑 Access → 📥 Enter Access Token</b>.\n\n"+
				"⚠️ This token is <b>permanent</b> and <b>multi-use</b> — anyone with it can request "+
				"delegated access to your clients. You'll approve or reject each request individually.\n\n"+
				"Use <b>🔄 Regenerate Token</b> if you want to invalidate the current one.",
			token,
		),
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	)
	return err
}

func handleMyDelegates(m *telegram.NewMessage) error {
	userID := m.SenderID()
	delegates, err := database.ListDelegates(userID)
	if err != nil {
		sendTraceback(fmt.Sprintf("ListDelegates for user %d: %v", userID, err))
		_, err := m.Reply("❌ <b>Failed to load delegates, please try again.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB})
		return err
	}

	text, kb := buildDelegateListPage(delegates)
	_, err = m.Reply(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
	return err
}

func buildDelegateListPage(delegates []int64) (string, *telegram.KeyboardBuilder) {
	kb := telegram.NewKeyboard()

	if len(delegates) == 0 {
		kb.AddRow(telegram.Button.Data("— nobody yet —", "actok:noop"))
		return "👥 <b>Using Your Token</b>\n\nNobody is currently using your accounts via your token.", kb
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("👥 <b>Using Your Token</b> — <code>%d</code> total\n\n", len(delegates)))
	for i, d := range delegates {
		sb.WriteString(fmt.Sprintf("%d. <code>%d</code>\n", i+1, d))
		kb.AddRow(telegram.Button.Data(
			fmt.Sprintf("⛔ Terminate #%d", i+1),
			fmt.Sprintf("actok:termask:%d", d),
		))
	}
	return sb.String(), kb
}

// ---- Enter token flow --------------------------------------------------------

func handleEnterTokenPrompt(m *telegram.NewMessage) error {
	userID := m.SenderID()

	ctx, ok := guardProcessWithKeyboard(
		m,
		"Enter Access Token",
		telegram.NewKeyboard().AddRow(telegram.Button.Text(BtnBackToAccess)),
	)
	if !ok {
		return nil
	}
	cg.SetListening(userID)

	_, err := m.Reply(
		"📥 <b>Enter Access Token</b>\n\n"+
			"Send the access token you were given.\n\n"+
			"<i>Send /cancel to abort.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessBackKB},
	)
	if err != nil {
		ps.done(userID)
		return err
	}

	go enterTokenStep(ctx, userID)
	return nil
}

func enterTokenStep(ctx context.Context, userID int64) {
	defer func() {
		if r := recover(); r != nil {
			sendTraceback(fmt.Sprintf("panic in enterTokenStep: %v", r))
		}
		ps.done(userID)
		cg.StopListening(userID)
	}()

	msgs, ok := cg.Recv(userID, 5*time.Minute)
	if !ok {
		if ctx.Err() == nil {
			c.SendMessage(userID,
				"⏰ <b>Timed out.</b> Tap <b>📥 Enter Access Token</b> to try again.",
				&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB},
			)
		}
		return
	}
	if ctx.Err() != nil {
		return
	}
	if strings.TrimSpace(msgs[0].Text()) == BtnBackToAccess {
		c.SendMessage(userID,
			"<b>🔑 Access Panel</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB},
		)
		return
	}

	token, valid := normalizeAccessToken(msgs[0].Text())
	if !valid {
c.SendMessage(userID, "❌ <b>Invalid token format.</b> Tokens must look like <code>ABCD-EFGH-IJKL-MNOP</code>.",
	&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB})
		return
	}

	grantorID, found, err := database.FindAccessTokenOwner(token)
	if err != nil {
		sendTraceback(fmt.Sprintf("FindAccessTokenOwner for user %d: %v", userID, err))
		c.SendMessage(userID, "❌ <b>Something went wrong, please try again.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB})
		return
	}
	if !found {
		c.SendMessage(userID, "❌ <b>Invalid token.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB})
		return
	}
	if grantorID == userID {
		c.SendMessage(userID, "❌ <b>You can't claim your own token.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB})
		return
	}
	if !rememberPendingAccessRequest(userID, pendingAccessRequest{kind: "token"}) {
		c.SendMessage(userID,
			"⏳ <b>You already have a pending access request.</b>\n\nPlease wait for it to be approved or rejected.",
			&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB},
		)
		return
	}

	c.SendMessage(userID,
		"🎟 <b>Access request sent!</b>\n\nWaiting for the token owner to approve your request. You'll be notified here.",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: accessPanelKB},
	)

	sendApprovalCard(
		grantorID,
		userID,
		"🎟 Access Token Claim Request",
		fmt.Sprintf("accreq:token:%d:%d", grantorID, userID),
	)
}

// ---- Approval card -----------------------------------------------------------

func sendApprovalCard(recipientID, targetUserID int64, title, approveDataPrefix string) {
	info, photo := fetchUserCard(targetUserID, title)

	kb := telegram.NewKeyboard()
	kb.AddRow(
		telegram.Button.Data("✅ Accept", approveDataPrefix+":accept"),
		telegram.Button.Data("❌ Reject", approveDataPrefix+":reject"),
	)

	if len(photo) > 0 {
		if _, err := c.SendMedia(recipientID, photo, &telegram.MediaOptions{
			Caption:     info,
			ParseMode:   telegram.HTML,
			ReplyMarkup: kb.Build(),
			FileName:    fmt.Sprintf("profile_%d.jpg", targetUserID),
		}); err == nil {
			return
		} else {
			sendTraceback(fmt.Sprintf("sendApprovalCard photo send failed for %d: %v", targetUserID, err))
		}
	}

	c.SendMessage(recipientID, info, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
}

func fetchUserCard(userID int64, title string) (caption string, photoBytes []byte) {
	user, err := c.GetUser(userID)

	name := fmt.Sprintf("ID %d", userID)
	username := "—"
	bio := "—"

	if err == nil && user != nil {
		full := strings.TrimSpace(user.FirstName + " " + user.LastName)
		if full != "" {
			name = full
		}
		if user.Username != "" {
			username = "@" + user.Username
		}
	}

	caption = fmt.Sprintf(
		"%s\n\n"+
			"👤 <b>Name:</b> %s\n"+
			"📛 <b>Username:</b> %s\n"+
			"🆔 <b>ID:</b> <code>%d</code>\n"+
			"📝 <b>Bio:</b> %s",
		escapeHTML(title), escapeHTML(name), escapeHTML(username), userID, escapeHTML(bio),
	)

	photos, perr := c.GetProfilePhotos(userID, &telegram.PhotosOptions{Limit: 1})
	if perr != nil || len(photos) == 0 {
		return caption, nil
	}

	var buf bytes.Buffer
	if _, derr := c.DownloadMedia(&photos[0], &telegram.DownloadOptions{Buffer: &buf}); derr == nil {
		photoBytes = buf.Bytes()
	}

	return caption, photoBytes
}

// ---- Callback handling -------------------------------------------------------

type pendingRejection struct {
	targetUserID int64
	grantorID    int64
}

var (
	rejectMu      sync.Mutex
	pendingReject = map[int64]pendingRejection{}
)

func handleAccessCallback(cb *telegram.CallbackQuery) error {
	data := cb.DataString()
	reviewerID := cb.GetSenderID()

	switch {
	case strings.HasPrefix(data, "accreq:send:"):
		return handleSendAccessRequest(cb, data)

	case strings.HasPrefix(data, "accreq:direct:") && strings.HasSuffix(data, ":accept"):
		return handleDirectAccept(cb, data)

	case strings.HasPrefix(data, "accreq:direct:") && strings.HasSuffix(data, ":reject"):
		return handleDirectRejectAsk(cb, data, reviewerID)

	case strings.HasPrefix(data, "accreq:token:") && strings.HasSuffix(data, ":accept"):
		return handleTokenAccept(cb, data)

	case strings.HasPrefix(data, "accreq:token:") && strings.HasSuffix(data, ":reject"):
		return handleTokenRejectAsk(cb, data, reviewerID)

	case data == "actok:regenask":
		return handleRegenAsk(cb)
	case data == "actok:regen":
		return handleRegenConfirm(cb)
	case data == "actok:regencancel":
		return handleRegenCancel(cb)

	case strings.HasPrefix(data, "actok:termask:"):
		return handleTerminateAsk(cb, data)
	case strings.HasPrefix(data, "actok:term:"):
		return handleTerminateConfirm(cb, data)
	case strings.HasPrefix(data, "actok:termcancel"):
		return handleTerminateCancel(cb)

	case data == "actok:noop":
		_, err := cb.Answer("")
		return err
	}
	return nil
}

// ---- Regenerate flow ---------------------------------------------------------

func handleRegenAsk(cb *telegram.CallbackQuery) error {
	kb := telegram.NewKeyboard()
	kb.AddRow(
		telegram.Button.Data("✅ Yes, regenerate", "actok:regen"),
		telegram.Button.Data("❌ No", "actok:regencancel"),
	)
	_, err := cb.Edit(
		"⚠️ <b>Regenerate your access token?</b>\n\n"+
			"Your current token will be invalidated. Anyone who had it will need the new token to request access again.\n\n"+
			"<i>Existing delegates are unaffected — they keep their access.</i>",
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	)
	return err
}

func handleRegenConfirm(cb *telegram.CallbackQuery) error {
	userID := cb.GetSenderID()

	token, err := database.RegenerateAccessToken(userID)
	if err != nil {
		sendTraceback(fmt.Sprintf("RegenerateAccessToken for user %d: %v", userID, err))
		_, err := cb.Answer("Failed to regenerate token, please try again.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Data("🔄 Regenerate Token", "actok:regenask"))

	if _, err := cb.Edit(
		fmt.Sprintf(
			"✅ <b>Token regenerated!</b>\n\n"+
				"<code>%s</code>\n\n"+
				"Your old token is now invalid. Share this new one with anyone you want to give access to.",
			token,
		),
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	); err != nil {
		return err
	}
	_, err = cb.Answer("Regenerated.")
	return err
}

func handleRegenCancel(cb *telegram.CallbackQuery) error {
	userID := cb.GetSenderID()
	token, err := database.GetOrCreateAccessToken(userID)
	if err != nil {
		_, err := cb.Answer("Failed to load token.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	kb := telegram.NewKeyboard()
	kb.AddRow(telegram.Button.Data("🔄 Regenerate Token", "actok:regenask"))

	if _, err := cb.Edit(
		fmt.Sprintf(
			"🎟 <b>Your Access Token</b>\n\n<code>%s</code>\n\n"+
				"Share this with anyone you want to give access to.",
			token,
		),
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	); err != nil {
		return err
	}
	_, err = cb.Answer("Cancelled.")
	return err
}

// ---- Terminate flow ----------------------------------------------------------

func handleTerminateCancel(cb *telegram.CallbackQuery) error {
	userID := cb.GetSenderID()
	delegates, err := database.ListDelegates(userID)
	if err != nil {
		sendTraceback(fmt.Sprintf("ListDelegates for user %d: %v", userID, err))
		_, err := cb.Answer("Failed to load delegates, please try again.", &telegram.CallbackOptions{Alert: true})
		return err
	}
	text, kb := buildDelegateListPage(delegates)
	if _, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()}); err != nil {
		return err
	}
	_, err = cb.Answer("Cancelled.")
	return err
}

func handleTerminateAsk(cb *telegram.CallbackQuery, data string) error {
	var delegateID int64
	fmt.Sscanf(strings.TrimPrefix(data, "actok:termask:"), "%d", &delegateID)

	kb := telegram.NewKeyboard()
	kb.AddRow(
		telegram.Button.Data("✅ Yes, terminate", fmt.Sprintf("actok:term:%d", delegateID)),
		telegram.Button.Data("❌ No", "actok:termcancel"),
	)
	_, err := cb.Edit(
		fmt.Sprintf(
			"⚠️ <b>Terminate access for <code>%d</code>?</b>\n\nThey will immediately lose access to your accounts.",
			delegateID,
		),
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	)
	return err
}

func handleTerminateConfirm(cb *telegram.CallbackQuery, data string) error {
	var delegateID int64
	fmt.Sscanf(strings.TrimPrefix(data, "actok:term:"), "%d", &delegateID)
	grantorID := cb.GetSenderID()

	if err := database.RemoveDelegate(grantorID, delegateID); err != nil {
		sendTraceback(fmt.Sprintf("RemoveDelegate(%d,%d): %v", grantorID, delegateID, err))
	}
	c.SendMessage(delegateID,
		"⛔ <b>Your access to that account's clients has been terminated by the owner.</b>",
		&telegram.SendOptions{ParseMode: telegram.HTML},
	)

	delegates, _ := database.ListDelegates(grantorID)
	text, kb := buildDelegateListPage(delegates)
	if _, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()}); err != nil {
		return err
	}
	_, err := cb.Answer("Terminated.")
	return err
}

// ---- Access request (direct bot auth) ----------------------------------------

func handleSendAccessRequest(cb *telegram.CallbackQuery, data string) error {
	var requesterID int64
	fmt.Sscanf(strings.TrimPrefix(data, "accreq:send:"), "%d", &requesterID)
	if requesterID == 0 {
		_, err := cb.Answer("Invalid request.")
		return err
	}
	if cb.GetSenderID() != requesterID {
		_, err := cb.Answer("This request button belongs to another user.", &telegram.CallbackOptions{Alert: true})
		return err
	}
	if isAuthorizedOrOwner(requesterID) {
		_, err := cb.Answer("You're already authorized — send /start.")
		return err
	}
	if config.Owner == nil {
		_, err := cb.Answer("Owner not configured yet, try again later.", &telegram.CallbackOptions{Alert: true})
		return err
	}
	if !rememberPendingAccessRequest(requesterID, pendingAccessRequest{kind: "bot"}) {
		_, err := cb.Answer("You already have a pending access request.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	sendApprovalCard(
		config.Owner.ID,
		requesterID,
		"🔐 Bot Authorization Request",
		fmt.Sprintf("accreq:direct:%d", requesterID),
	)

	if _, err := cb.Edit(
		"🔐 <b>Bot authorization request sent!</b>\n\nWaiting for the bot owner to approve you. You'll be notified here.",
		&telegram.SendOptions{ParseMode: telegram.HTML},
	); err != nil {
		clearPendingAccessRequest(requesterID)
		return err
	}
	_, err := cb.Answer("Request sent.")
	return err
}

func handleDirectAccept(cb *telegram.CallbackQuery, data string) error {
	rest := strings.TrimSuffix(strings.TrimPrefix(data, "accreq:direct:"), ":accept")
	var targetUserID int64
	fmt.Sscanf(rest, "%d", &targetUserID)
	if targetUserID == 0 {
		_, err := cb.Answer("Invalid request.")
		return err
	}
	if config.Owner == nil || cb.GetSenderID() != config.Owner.ID {
		_, err := cb.Answer("Only the bot owner can approve this.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	if err := database.AddAuthorizedUser(targetUserID); err != nil {
		sendTraceback(fmt.Sprintf("AddAuthorizedUser(%d): %v", targetUserID, err))
		_, err := cb.Answer("Failed to approve, try again.")
		return err
	}

	finalizeApprovalCard(cb, true, "")
	notifyApprovalResult(targetUserID, true, "")
	clearPendingAccessRequest(targetUserID)
	_, err := cb.Answer("Approved.")
	return err
}

func handleDirectRejectAsk(cb *telegram.CallbackQuery, data string, reviewerID int64) error {
	rest := strings.TrimSuffix(strings.TrimPrefix(data, "accreq:direct:"), ":reject")
	var targetUserID int64
	fmt.Sscanf(rest, "%d", &targetUserID)
	if targetUserID == 0 {
		_, err := cb.Answer("Invalid request.")
		return err
	}
	if config.Owner == nil || cb.GetSenderID() != config.Owner.ID {
		_, err := cb.Answer("Only the bot owner can reject this.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	askForRejectionReason(reviewerID, pendingRejection{targetUserID: targetUserID})

	if _, err := cb.Edit(
		"❓ <b>Reply with a reason for rejecting this request</b> (or send <code>-</code> for no reason).",
		&telegram.SendOptions{ParseMode: telegram.HTML},
	); err != nil {
		return err
	}
	_, err := cb.Answer("")
	return err
}

// handleTokenAccept: "accreq:token:<grantorID>:<targetUserID>:accept"
func handleTokenAccept(cb *telegram.CallbackQuery, data string) error {
	grantorID, targetUserID, ok := parseTokenCallback(data, ":accept")
	if !ok {
		_, err := cb.Answer("Invalid request.")
		return err
	}
	if cb.GetSenderID() != grantorID {
		_, err := cb.Answer("Only the token owner can approve this.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	if err := database.AddDelegate(grantorID, targetUserID); err != nil {
		sendTraceback(fmt.Sprintf("AddDelegate(%d,%d): %v", grantorID, targetUserID, err))
		_, err := cb.Answer("Failed to approve, try again.")
		return err
	}

	finalizeApprovalCard(cb, true, "")
	notifyApprovalResult(targetUserID, true, "")
	clearPendingAccessRequest(targetUserID)
	_, err := cb.Answer("Approved.")
	return err
}

func handleTokenRejectAsk(cb *telegram.CallbackQuery, data string, reviewerID int64) error {
	grantorID, targetUserID, ok := parseTokenCallback(data, ":reject")
	if !ok {
		_, err := cb.Answer("Invalid request.")
		return err
	}
	if cb.GetSenderID() != grantorID {
		_, err := cb.Answer("Only the token owner can reject this.", &telegram.CallbackOptions{Alert: true})
		return err
	}

	askForRejectionReason(reviewerID, pendingRejection{targetUserID: targetUserID, grantorID: grantorID})

	if _, err := cb.Edit(
		"❓ <b>Reply with a reason for rejecting this request</b> (or send <code>-</code> for no reason).",
		&telegram.SendOptions{ParseMode: telegram.HTML},
	); err != nil {
		return err
	}
	_, err := cb.Answer("")
	return err
}

// parseTokenCallback: "accreq:token:<grantorID>:<targetUserID>:<suffix>"
func parseTokenCallback(data, suffix string) (grantorID, targetUserID int64, ok bool) {
	rest := strings.TrimSuffix(strings.TrimPrefix(data, "accreq:token:"), suffix)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[0], "%d", &grantorID); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &targetUserID); err != nil {
		return 0, 0, false
	}
	return grantorID, targetUserID, grantorID != 0 && targetUserID != 0
}

func finalizeApprovalCard(cb *telegram.CallbackQuery, accepted bool, reason string) {
	verdict := "✅ <b>Approved</b>"
	if !accepted {
		verdict = "❌ <b>Rejected</b>"
		if reason != "" {
			verdict += fmt.Sprintf("\n<b>Reason:</b> %s", escapeHTML(reason))
		}
	}
	if _, err := cb.Edit(verdict, &telegram.SendOptions{ParseMode: telegram.HTML}); err != nil {
		sendTraceback(fmt.Sprintf("finalizeApprovalCard edit failed: %v", err))
	}
}

func notifyApprovalResult(targetUserID int64, accepted bool, reason string) {
	if accepted {
		c.SendMessage(targetUserID,
			"✅ <b>Your access request has been approved!</b>\n\nSend /start to begin.",
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
		return
	}
	text := "❌ <b>Your access request was rejected.</b>"
	if reason != "" {
		text += fmt.Sprintf("\n\n<b>Reason:</b> %s", escapeHTML(reason))
	}
	c.SendMessage(targetUserID, text, &telegram.SendOptions{ParseMode: telegram.HTML})
}

func askForRejectionReason(reviewerID int64, p pendingRejection) {
	rejectMu.Lock()
	pendingReject[reviewerID] = p
	rejectMu.Unlock()

	cg.SetListening(reviewerID)
	go func() {
		defer cg.StopListening(reviewerID)

		msgs, ok := cg.Recv(reviewerID, 5*time.Minute)
		rejectMu.Lock()
		pend, stillPending := pendingReject[reviewerID]
		delete(pendingReject, reviewerID)
		rejectMu.Unlock()

		if !stillPending {
			return
		}

		reason := ""
		if ok && len(msgs) > 0 {
			reason = strings.TrimSpace(msgs[0].Text())
			if reason == "-" {
				reason = ""
			}
		}

		notifyApprovalResult(pend.targetUserID, false, reason)
		clearPendingAccessRequest(pend.targetUserID)
		c.SendMessage(reviewerID,
			"❌ <b>Rejection sent.</b>",
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
	}()
}
