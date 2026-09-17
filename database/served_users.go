package database

func ServedUsers() ([]int64, error) {
	state, err := getBotState()
	if err != nil {
		return nil, err
	}

	return state.Users, nil
}

func IsServedUser(id int64) (bool, error) {
	state, err := getBotState()
	if err != nil {
		return false, err
	}

	_, ok := state.usersMap[id]
	return ok, nil
}

func AddServedUser(id int64) error {
	return modifyBotState(func(s *BotState) bool {
		var added bool

		s.Users, added = addUnique(s.Users, id)
		if added {
			s.usersMap[id] = struct{}{}
		}

		return added
	})
}

func RemoveServedUser(id int64) error {
	return modifyBotState(func(s *BotState) bool {
		var removed bool

		s.Users, removed = removeElement(s.Users, id)
		if removed {
			delete(s.usersMap, id)
		}

		return removed
	})
}
