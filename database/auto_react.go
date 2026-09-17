package database

func AddAutoReactChat(userID, chatID int64) error {
	err := modifyUserState(userID, func(s *UserState) bool {
		var added bool
		s.AutoReactChats, added = addUnique(s.AutoReactChats, chatID)
		return added
	})
	if err == nil {
		reactOwnersMu.Lock()
		reactOwners[chatID], _ = addUnique(reactOwners[chatID], userID)
		reactOwnersMu.Unlock()
	}
	return err
}

func RemoveAutoReactChat(userID, chatID int64) error {
	err := modifyUserState(userID, func(s *UserState) bool {
		var removed bool
		s.AutoReactChats, removed = removeElement(s.AutoReactChats, chatID)
		return removed
	})
	if err == nil {
		reactOwnersMu.Lock()
		reactOwners[chatID], _ = removeElement(reactOwners[chatID], userID)
		if len(reactOwners[chatID]) == 0 {
			delete(reactOwners, chatID)
		}
		reactOwnersMu.Unlock()
	}
	return err
}

func GetAutoReactChats(userID int64) ([]int64, error) {
	state, err := getUserState(userID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(state.AutoReactChats))
	copy(out, state.AutoReactChats)
	return out, nil
}

func IsAutoReactChat(userID, chatID int64) (bool, error) {
	state, err := getUserState(userID)
	if err != nil {
		return false, err
	}
	return contains(state.AutoReactChats, chatID), nil
}

func GetReactOwners(chatID int64) []int64 {
	reactOwnersMu.RLock()
	defer reactOwnersMu.RUnlock()
	out := make([]int64, len(reactOwners[chatID]))
	copy(out, reactOwners[chatID])
	return out
}
