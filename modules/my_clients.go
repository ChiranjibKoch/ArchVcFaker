package modules

import (
	"fmt"
	"strings"

	"tgmultibot/manager"

	"github.com/amarnathcjd/gogram/telegram"
)

const clientsPerPage = 15

func handleMyClients(m *telegram.NewMessage) error {
	userID := m.SenderID()
	ownerID := effectiveOwnerID(userID)
	clients := cm.GetClients(ownerID)

	if len(clients) == 0 {
		_, err := m.Reply(noClientsMsg(userID), &telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}

	text, kb := buildClientListPage(clients, 0)
	_, err := m.Reply(text, &telegram.SendOptions{
		ParseMode:   telegram.HTML,
		ReplyMarkup: kb.Build(),
	})
	return err
}

func handleClientCallback(cb *telegram.CallbackQuery) error {
	data := cb.DataString()
	userID := cb.GetSenderID()
	ownerID := effectiveOwnerID(userID)

	switch {
	case strings.HasPrefix(data, "client:page:"):
		var page int
		fmt.Sscanf(strings.TrimPrefix(data, "client:page:"), "%d", &page)
		return cbRefreshPage(cb, ownerID, page)

	case strings.HasPrefix(data, "client:info:"):
		var accountID int64
		fmt.Sscanf(strings.TrimPrefix(data, "client:info:"), "%d", &accountID)
		return cbShowDetail(cb, userID, ownerID, accountID)

	case strings.HasPrefix(data, "client:remove:"):
		if !isPersonalCtx(userID) {
			_, err := cb.Answer("❌ Remove is only available in Personal context.", &telegram.CallbackOptions{Alert: true})
			return err
		}
		var accountID int64
		fmt.Sscanf(strings.TrimPrefix(data, "client:remove:"), "%d", &accountID)
		return cbConfirmRemove(cb, userID, accountID)

	case strings.HasPrefix(data, "client:remove_confirm:"):
		if !isPersonalCtx(userID) {
			_, err := cb.Answer("❌ Remove is only available in Personal context.", &telegram.CallbackOptions{Alert: true})
			return err
		}
		var accountID int64
		fmt.Sscanf(strings.TrimPrefix(data, "client:remove_confirm:"), "%d", &accountID)
		return cbDoRemove(cb, userID, accountID)

	case strings.HasPrefix(data, "client:otp:"):
		if !isPersonalCtx(userID) {
			_, err := cb.Answer("❌ Get OTP is only available in Personal context.", &telegram.CallbackOptions{Alert: true})
			return err
		}
		var accountID int64
		fmt.Sscanf(strings.TrimPrefix(data, "client:otp:"), "%d", &accountID)
		return handleOTPCallback(cb, userID, accountID)

	case strings.HasPrefix(data, "client:back:"):
		var page int
		fmt.Sscanf(strings.TrimPrefix(data, "client:back:"), "%d", &page)
		return cbRefreshPage(cb, ownerID, page)

	case data == "client:noop":
		_, err := cb.Answer("")
		return err
	}

	return nil
}

func buildClientListPage(clients []*manager.ManagedClient, page int) (string, *telegram.KeyboardBuilder) {
	total := len(clients)
	totalPages := (total + clientsPerPage - 1) / clientsPerPage
	if page >= totalPages {
		page = totalPages - 1
	}

	start := page * clientsPerPage
	end := start + clientsPerPage
	if end > total {
		end = total
	}
	pageClients := clients[start:end]

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📋 <b>My Clients</b> — <code>%d</code> total\n\n", total))

	kb := telegram.NewKeyboard()

	for i, mc := range pageClients {
		globalNum := start + i + 1
		me := mc.Client.Me()
		var line, btnText string
		if me == nil {
			line = fmt.Sprintf("%d. ❓ unknown", globalNum)
			btnText = fmt.Sprintf("%d. unknown", globalNum)
		} else {
			line = formatClientLine(globalNum, me)
			btnText = fmt.Sprintf("%d. %s", globalNum, clientShortName(me))
		}
		sb.WriteString(line + "\n")
		kb.AddRow(telegram.Button.Data(btnText, fmt.Sprintf("client:info:%d", mc.AccountID)))
	}

	if totalPages > 1 {
		var paginationRow []telegram.KeyboardButton
		if page > 0 {
			paginationRow = append(paginationRow,
				telegram.Button.Data("◀️ Prev", fmt.Sprintf("client:page:%d", page-1)))
		}
		paginationRow = append(paginationRow,
			telegram.Button.Data(fmt.Sprintf("%d / %d", page+1, totalPages), "client:noop"))
		if page < totalPages-1 {
			paginationRow = append(paginationRow,
				telegram.Button.Data("Next ▶️", fmt.Sprintf("client:page:%d", page+1)))
		}
		kb.AddRow(paginationRow...)
	}

	return sb.String(), kb
}

func findClientByAccountID(ownerID, accountID int64) (*manager.ManagedClient, int) {
	for i, mc := range cm.GetClients(ownerID) {
		if mc.AccountID == accountID {
			return mc, i
		}
	}
	return nil, -1
}

func cbRefreshPage(cb *telegram.CallbackQuery, ownerID int64, page int) error {
	clients := cm.GetClients(ownerID)
	if len(clients) == 0 {
		_, err := cb.Edit("📋 <b>My Clients</b>\n\nNo clients found.",
			&telegram.SendOptions{ParseMode: telegram.HTML})
		return err
	}
	text, kb := buildClientListPage(clients, page)
	_, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
	return err
}

func cbShowDetail(cb *telegram.CallbackQuery, userID, ownerID, accountID int64) error {
	mc, idx := findClientByAccountID(ownerID, accountID)
	if mc == nil {
		_, err := cb.Answer("Client not found.")
		return err
	}

	me := mc.Client.Me()
	if me == nil {
		_, err := cb.Answer("Failed to fetch client info.")
		return err
	}

	username := "—"
	if me.Username != "" {
		username = "@" + me.Username
	}
	phone := "—"
	if me.Phone != "" {
		phone = "+" + me.Phone
	}

	backPage := idx / clientsPerPage

	text := fmt.Sprintf(
		"👤 <b>Client #%d</b>\n\n"+
			"🔖 <b>Name:</b> %s\n"+
			"📛 <b>Username:</b> %s\n"+
			"📞 <b>Phone:</b> <code>%s</code>\n"+
			"🆔 <b>ID:</b> <code>%d</code>",
		idx+1,
		strings.TrimSpace(me.FirstName+" "+me.LastName),
		username, phone, me.ID,
	)

	kb := telegram.NewKeyboard()
	personal := isPersonalCtx(userID)
	if personal {
		kb.AddRow(telegram.Button.Data("📲 Get OTP", fmt.Sprintf("client:otp:%d", accountID)))
		kb.AddRow(
			telegram.Button.Data("🗑 Remove", fmt.Sprintf("client:remove:%d", accountID)),
			telegram.Button.Data("🔙 Back", fmt.Sprintf("client:back:%d", backPage)),
		)
	} else {
		kb.AddRow(telegram.Button.Data("🔙 Back", fmt.Sprintf("client:back:%d", backPage)))
	}

	_, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
	return err
}

func cbConfirmRemove(cb *telegram.CallbackQuery, userID, accountID int64) error {
	mc, idx := findClientByAccountID(userID, accountID)
	if mc == nil {
		_, err := cb.Answer("Client not found.")
		return err
	}

	name := clientShortName(mc.Client.Me())
	kb := telegram.NewKeyboard()
	kb.AddRow(
		telegram.Button.Data("✅ Yes, remove", fmt.Sprintf("client:remove_confirm:%d", accountID)),
		telegram.Button.Data("❌ Cancel", fmt.Sprintf("client:info:%d", accountID)),
	)

	_, err := cb.Edit(
		fmt.Sprintf("⚠️ <b>Remove client #%d (%s)?</b>\n\nThis will disconnect the session immediately.", idx+1, name),
		&telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()},
	)
	return err
}

func cbDoRemove(cb *telegram.CallbackQuery, userID, accountID int64) error {
	_, idx := findClientByAccountID(userID, accountID)
	if idx == -1 {
		_, err := cb.Answer("Client not found.")
		return err
	}

	backPage := idx / clientsPerPage

	if err := cm.RemoveClient(userID, idx); err != nil {
		_, err := cb.Answer("Failed to remove client.")
		return err
	}

	clients := cm.GetClients(userID)
	if len(clients) == 0 {
		_, err := cb.Edit(
			"✅ <b>Client removed.</b>\n\nYou have no more clients. Tap <b>➕ Add Client</b> to add one.",
			&telegram.SendOptions{ParseMode: telegram.HTML},
		)
		return err
	}

	totalPages := (len(clients) + clientsPerPage - 1) / clientsPerPage
	if backPage >= totalPages {
		backPage = totalPages - 1
	}

	text, kb := buildClientListPage(clients, backPage)
	_, err := cb.Edit(text, &telegram.SendOptions{ParseMode: telegram.HTML, ReplyMarkup: kb.Build()})
	return err
}

func formatClientLine(num int, me *telegram.UserObj) string {
	var parts []string
	if me.Username != "" {
		parts = append(parts, "@"+me.Username)
	}
	if me.Phone != "" {
		parts = append(parts, "+"+me.Phone)
	}
	info := strings.Join(parts, " ")
	if info == "" {
		info = fmt.Sprintf("ID:%d", me.ID)
	}
	return fmt.Sprintf("%d. %s", num, info)
}

func clientShortName(me *telegram.UserObj) string {
	if me == nil {
		return "unknown"
	}
	if me.Username != "" {
		return "@" + me.Username
	}
	if me.Phone != "" {
		return "+" + me.Phone
	}
	return fmt.Sprintf("ID:%d", me.ID)
}
