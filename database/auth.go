package database

// IsAuthorized reports whether userID may use the bot — either because the
// bot owner explicitly authorized them, or because they hold an active
// delegation (i.e. they redeemed someone's access token and were
// approved). The bot owner himself is intentionally NOT checked here;
// callers should always allow the owner separately via config.OwnerID,
// since the owner is authorized by definition and never stored in this list.
func IsAuthorized(userID int64) (bool, error) {
	state, err := getBotState()
	if err != nil {
		return false, err
	}
	if _, ok := state.authorizedMap[userID]; ok {
		return true, nil
	}
	return IsDelegate(userID), nil
}

// AddAuthorizedUser grants userID permanent bot access (independent of any
// access token / delegation).
func AddAuthorizedUser(userID int64) error {
	return modifyBotState(func(s *BotState) bool {
		var added bool
		s.AuthorizedUsers, added = addUnique(s.AuthorizedUsers, userID)
		if added {
			s.authorizedMap[userID] = struct{}{}
		}
		return added
	})
}

// RemoveAuthorizedUser revokes userID's standing bot access. This does NOT
// affect any delegations they might separately hold.
func RemoveAuthorizedUser(userID int64) error {
	return modifyBotState(func(s *BotState) bool {
		var removed bool
		s.AuthorizedUsers, removed = removeElement(s.AuthorizedUsers, userID)
		if removed {
			delete(s.authorizedMap, userID)
		}
		return removed
	})
}

// ListAuthorizedUsers returns every explicitly authorized user ID.
func ListAuthorizedUsers() ([]int64, error) {
	state, err := getBotState()
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(state.AuthorizedUsers))
	copy(out, state.AuthorizedUsers)
	return out, nil
}
