package database

// GetUserClients returns all ClientInfo records for a user.
func GetUserClients(userID int64) ([]ClientInfo, error) {
	state, err := getUserState(userID)
	if err != nil {
		return nil, err
	}
	// Return a copy so callers cannot mutate the cache.
	out := make([]ClientInfo, len(state.Clients))
	copy(out, state.Clients)
	return out, nil
}

// GetActiveUserClients returns only non-disabled ClientInfo records.
func GetActiveUserClients(userID int64) ([]ClientInfo, error) {
	all, err := GetUserClients(userID)
	if err != nil {
		return nil, err
	}
	active := all[:0:0] // zero-len slice with pre-allocated cap
	for _, c := range all {
		if !c.Disabled {
			active = append(active, c)
		}
	}
	return active, nil
}

// UpsertUserClient adds a new client or replaces an existing one (matched by
// AccountID). The session is encoded before storage.
func UpsertUserClient(userID int64, info ClientInfo) error {
	return modifyUserState(userID, func(s *UserState) bool {
		if idx, ok := s.clientIdx[info.AccountID]; ok {
			s.Clients[idx] = info
		} else {
			s.clientIdx[info.AccountID] = len(s.Clients)
			s.Clients = append(s.Clients, info)
		}
		return true
	})
}

// DisableUserClient marks a client as disabled without removing it from the DB.
// reason should be a short description, e.g. "SESSION_REVOKED".
func DisableUserClient(userID, accountID int64, reason string) error {
	return modifyUserState(userID, func(s *UserState) bool {
		idx, ok := s.clientIdx[accountID]
		if !ok {
			return false
		}
		if s.Clients[idx].Disabled && s.Clients[idx].DisabledReason == reason {
			return false // nothing changed
		}
		s.Clients[idx].Disabled = true
		s.Clients[idx].DisabledReason = reason
		return true
	})
}

// RemoveUserClient permanently removes a client by accountID.
func RemoveUserClient(userID, accountID int64) error {
	return modifyUserState(userID, func(s *UserState) bool {
		idx, ok := s.clientIdx[accountID]
		if !ok {
			return false
		}
		s.Clients = append(s.Clients[:idx], s.Clients[idx+1:]...)
		// Rebuild the index since positions shifted.
		buildClientIndex(s)
		return true
	})
}
