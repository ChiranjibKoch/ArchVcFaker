package manager

import (
	"fmt"
	"sort"
	"strings"

	"github.com/amarnathcjd/gogram/telegram"
)

// ErrorReason classifies a Telegram API error into a human-readable category.
type ErrorReason string

const (
	ReasonBannedInChannel ErrorReason = "banned_in_channel"
	ReasonNotJoined       ErrorReason = "not_joined"
	ReasonFrozen          ErrorReason = "frozen"
	ReasonSessionDead     ErrorReason = "session_dead"
	ReasonFloodWait       ErrorReason = "flood_wait"
	ReasonPeerFlood       ErrorReason = "peer_flood"
	ReasonNoActiveCall    ErrorReason = "no_active_call"
	ReasonUserBusy        ErrorReason = "user_busy"
	ReasonSearchFailed    ErrorReason = "search_failed"
	ReasonNoTrackFound    ErrorReason = "no_track_found"
	ReasonOther           ErrorReason = "other"
)

// ErrorGroup groups failed account IDs under a single reason.
type ErrorGroup struct {
	Reason     ErrorReason
	AccountIDs []int64
}

// ErrorSummary is the result of classifyBulkErrors — a categorized summary
// suitable for display in a Telegram message.
type ErrorSummary struct {
	Groups   []ErrorGroup
	Other    []AccountError
	Total    int
	Failed   int
	Retried  int // how many were retried (FLOOD_WAIT that were slept+retried)
}

// AccountError pairs an account ID with its raw error.
type AccountError struct {
	AccountID int64
	Err       error
}

// ClassifyError inspects a Telegram API error and returns a human-readable
// reason. For display purposes it returns short, user-friendly messages.
func ClassifyError(err error) ErrorReason {
	if err == nil {
		return ""
	}

	msg := strings.ToUpper(err.Error())

	switch {
	case strings.Contains(msg, "AUTH_KEY_UNREGISTERED") ||
		strings.Contains(msg, "AUTH_KEY_INVALID") ||
		strings.Contains(msg, "SESSION_REVOKED") ||
		strings.Contains(msg, "SESSION_EXPIRED") ||
		strings.Contains(msg, "USER_DEACTIVATED") ||
		strings.Contains(msg, "USER_DEACTIVATED_BAN") ||
		strings.Contains(msg, "PHONE_NUMBER_BANNED") ||
		strings.Contains(msg, "AUTH_KEY_DUPLICATED") ||
		strings.Contains(msg, "ACCOUNT_BANNED"):
		return ReasonSessionDead

	case strings.Contains(msg, "FROZEN") ||
		strings.Contains(msg, "FROZEN_METHOD_INVALID"):
		return ReasonFrozen

	case strings.Contains(msg, "CHANNEL_PRIVATE") ||
		strings.Contains(msg, "USER_NOT_PARTICIPANT"):
		return ReasonNotJoined

	case strings.Contains(msg, "USER_BANNED_IN_CHANNEL") ||
		strings.Contains(msg, "BANNED_RIGHTS"):
		return ReasonBannedInChannel

	case strings.Contains(msg, "FLOOD_WAIT"):
		return ReasonFloodWait

	case strings.Contains(msg, "PEER_FLOOD") ||
		strings.Contains(msg, "USERS_TOO_MUCH"):
		return ReasonPeerFlood

	case strings.Contains(msg, "NO ACTIVE GROUP CALL") ||
		strings.Contains(msg, "IS CLOSED") ||
		strings.Contains(msg, "GROUPCALL_INVALID") ||
		strings.Contains(msg, "NO_ACTIVE_GROUP_CALL"):
		return ReasonNoActiveCall

	case strings.Contains(msg, "USER_IS_BUSY"):
		return ReasonUserBusy

	default:
		return ReasonOther
	}
}

// ReasonHint returns a short user-facing hint for each error reason.
func ReasonHint(r ErrorReason) string {
	switch r {
	case ReasonBannedInChannel:
		return "banned in this channel/group"
	case ReasonNotJoined:
		return "haven't joined this channel yet — use <b>🔗 Join Channel</b> button first"
	case ReasonFrozen:
		return "your ID is frozen/suspended"
	case ReasonSessionDead:
		return "account terminated or banned"
	case ReasonFloodWait:
		return "rate limited by Telegram"
	case ReasonPeerFlood:
		return "too many requests — try again later"
	case ReasonNoActiveCall:
		return "no active voice chat in that channel"
	case ReasonUserBusy:
		return "user is busy on another call"
	case ReasonSearchFailed:
		return "failed to search — try again later"
	case ReasonNoTrackFound:
		return "no track found — try searching with a shorter title (max 30 chars)"
	default:
		return ""
	}
}

// FloodWaitSeconds extracts the wait duration from a FLOOD_WAIT error.
// Returns 0 if the error is not a FLOOD_WAIT.
func FloodWaitSeconds(err error) int {
	if err == nil {
		return 0
	}
	wait := telegram.GetFloodWait(err)
	if wait > 0 {
		return int(wait)
	}
	// Fallback: parse manually from message like "FLOOD_WAIT_3" or "Please wait 3 seconds"
	msg := err.Error()
	upper := strings.ToUpper(msg)

	if idx := strings.Index(upper, "FLOOD_WAIT"); idx != -1 {
		rest := msg[idx+len("FLOOD_WAIT"):]
		n := 0
		for _, c := range rest {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			} else {
				break
			}
		}
		if n > 0 {
			return n
		}
	}

	if idx := strings.Index(upper, "PLEASE WAIT"); idx != -1 {
		rest := msg[idx+len("PLEASE WAIT"):]
		n := 0
		for _, c := range rest {
			if c >= '0' && c <= '9' {
				n = n*10 + int(c-'0')
			} else if n > 0 {
				break
			}
		}
		if n > 0 {
			return n
		}
	}

	return 0
}

// SummarizeErrors takes a slice of AccountError and groups them by reason,
// producing a display-ready ErrorSummary.
func SummarizeErrors(errors []AccountError) ErrorSummary {
	if len(errors) == 0 {
		return ErrorSummary{}
	}

	grouped := make(map[ErrorReason][]int64)
	var others []AccountError

	for _, ae := range errors {
		reason := ClassifyError(ae.Err)
		if reason == ReasonOther {
			others = append(others, ae)
		} else {
			grouped[reason] = append(grouped[reason], ae.AccountID)
		}
	}

	var groups []ErrorGroup
	for reason, ids := range grouped {
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		groups = append(groups, ErrorGroup{Reason: reason, AccountIDs: ids})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Reason < groups[j].Reason })

	return ErrorSummary{
		Groups: groups,
		Other:  others,
		Total:  len(errors),
		Failed: len(errors),
	}
}

// FormatErrorSummary renders the ErrorSummary into a user-friendly HTML string
// for appending to a Telegram message. prefix is prepended (e.g. "<b>Errors:</b>").
func FormatErrorSummary(summary ErrorSummary, prefix string) string {
	if summary.Failed == 0 {
		return ""
	}

	var sb strings.Builder
	if prefix != "" {
		sb.WriteString(prefix)
		sb.WriteString("\n")
	}

	for _, g := range summary.Groups {
		hint := ReasonHint(g.Reason)
		count := len(g.AccountIDs)

		if g.Reason == ReasonSessionDead {
			sb.WriteString(fmt.Sprintf("\n🚫 <b>%d account(s)</b> %s\n", count, hint))
			sb.WriteString("   Accounts: ")
			for i, id := range g.AccountIDs {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("<code>%d</code>", id))
			}
			sb.WriteString("\n   <i>💡 Use <b>📋 My Clients</b> to remove these accounts.</i>")
		} else if g.Reason == ReasonFrozen {
			sb.WriteString(fmt.Sprintf("\n🧊 <b>%d account(s)</b> %s\n", count, hint))
			sb.WriteString("   Accounts: ")
			for i, id := range g.AccountIDs {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("<code>%d</code>", id))
			}
			sb.WriteString("\n   <i>💡 These accounts need to be unfrozen on my.telegram.org or removed.</i>")
		} else if g.Reason == ReasonNotJoined {
			sb.WriteString(fmt.Sprintf("\n🔒 <b>%d account(s)</b> %s\n", count, hint))
			sb.WriteString("   Accounts: ")
			for i, id := range g.AccountIDs {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("<code>%d</code>", id))
			}
		} else if g.Reason == ReasonBannedInChannel {
			sb.WriteString(fmt.Sprintf("\n🚫 <b>%d account(s)</b> %s\n", count, hint))
			sb.WriteString("   Accounts: ")
			for i, id := range g.AccountIDs {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("<code>%d</code>", id))
			}
		} else if g.Reason == ReasonNoActiveCall {
			sb.WriteString(fmt.Sprintf("\n🔇 <b>%d account(s)</b>: %s\n", count, hint))
		} else if g.Reason == ReasonFloodWait {
			sb.WriteString(fmt.Sprintf("\n⏳ <b>%d account(s)</b>: %s (waited and retried)\n", count, hint))
		} else if g.Reason == ReasonPeerFlood {
			sb.WriteString(fmt.Sprintf("\n⚠️ <b>%d account(s)</b> %s\n", count, hint))
			sb.WriteString("   Accounts: ")
			for i, id := range g.AccountIDs {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString(fmt.Sprintf("<code>%d</code>", id))
			}
			sb.WriteString("\n   <i>💡 Too many bulk actions. Wait a while before trying again.</i>")
		} else {
			sb.WriteString(fmt.Sprintf("\n❌ <b>%d account(s)</b>: %s\n", count, hint))
			for _, id := range g.AccountIDs {
				sb.WriteString(fmt.Sprintf("   <code>%d</code>\n", id))
			}
		}
	}

	for _, ae := range summary.Other {
		sb.WriteString(fmt.Sprintf("\n• <code>%d</code>: %s\n", ae.AccountID, truncateErr(ae.Err.Error(), 120)))
	}

	return sb.String()
}

// FormatErrorSummaryShort is a compact one-line-per-reason version for toast messages.
func FormatErrorSummaryShort(summary ErrorSummary) string {
	if summary.Failed == 0 {
		return ""
	}

	var parts []string
	for _, g := range summary.Groups {
		count := len(g.AccountIDs)
		hint := ReasonHint(g.Reason)
		if hint != "" {
			parts = append(parts, fmt.Sprintf("%d %s", count, hint))
		} else {
			parts = append(parts, fmt.Sprintf("%d %s", count, g.Reason))
		}
	}
	if len(summary.Other) > 0 {
		parts = append(parts, fmt.Sprintf("%d other errors", len(summary.Other)))
	}
	return strings.Join(parts, ", ")
}

func truncateErr(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

// IsSessionDead returns true if the error indicates the session should be removed.
func IsSessionDead(err error) bool {
	return ClassifyError(err) == ReasonSessionDead
}

// ShouldDisableClient returns true if the error is fatal and the client should
// be stopped/removed from runtime. This covers session-related errors and bans.
func ShouldDisableClient(err error) bool {
	reason := ClassifyError(err)
	return reason == ReasonSessionDead || reason == ReasonFrozen
}

// NewSearchFailedError returns a logical error for a failed search query.
func NewSearchFailedError(query string) error {
	return fmt.Errorf("search failed for query %q — try again later", query)
}

// NewNoTrackFoundError returns a logical error when no track is found for a title.
func NewNoTrackFoundError(title string) error {
	if len(title) > 30 {
		title = title[:30]
	}
	return fmt.Errorf("no track found for %q — try searching with a shorter title (max 30 chars)", title)
}

// IsSearchError returns true if the error is a search-related error.
func IsSearchError(err error) bool {
	if err == nil {
		return false
	}
	reason := ClassifyError(err)
	return reason == ReasonSearchFailed || reason == ReasonNoTrackFound
}
