package database

func AddAutoViewChat(userID, chatID int64) error {
	err := modifyUserState(userID, func(s *UserState) bool {
		var added bool
		s.AutoViewChats, added = addUnique(s.AutoViewChats, chatID)
		return added
	})
	if err == nil {
		viewOwnersMu.Lock()
		viewOwners[chatID], _ = addUnique(viewOwners[chatID], userID)
		viewOwnersMu.Unlock()
	}
	return err
}

func RemoveAutoViewChat(userID, chatID int64) error {
	err := modifyUserState(userID, func(s *UserState) bool {
		var removed bool
		s.AutoViewChats, removed = removeElement(s.AutoViewChats, chatID)
		return removed
	})
	if err == nil {
		viewOwnersMu.Lock()
		viewOwners[chatID], _ = removeElement(viewOwners[chatID], userID)
		if len(viewOwners[chatID]) == 0 {
			delete(viewOwners, chatID)
		}
		viewOwnersMu.Unlock()
	}
	return err
}

func GetAutoViewChats(userID int64) ([]int64, error) {
	state, err := getUserState(userID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(state.AutoViewChats))
	copy(out, state.AutoViewChats)
	return out, nil
}

func IsAutoViewChat(userID, chatID int64) (bool, error) {
	state, err := getUserState(userID)
	if err != nil {
		return false, err
	}
	return contains(state.AutoViewChats, chatID), nil
}

func GetViewOwners(chatID int64) []int64 {
	viewOwnersMu.RLock()
	defer viewOwnersMu.RUnlock()
	out := make([]int64, len(viewOwners[chatID]))
	copy(out, viewOwners[chatID])
	return out
}
