package database

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type BotState struct {
	ID    string  `bson:"_id"`
	Users []int64 `bson:"users"`

	AuthorizedUsers []int64 `bson:"auth,omitempty"`

	// runtime index for fast lookup
	usersMap      map[int64]struct{} `bson:"-"`
	authorizedMap map[int64]struct{} `bson:"-"`
}

const botStateCacheKey = "bot_state"

func newDefaultBotState() *BotState {
	s := &BotState{
		ID:    "global",
		Users: []int64{},
	}

	buildIndexes(s)
	return s
}

func getBotState() (*BotState, error) {
	if cached, found := dbCache.Get(botStateCacheKey); found {
		if state, ok := cached.(*BotState); ok {
			return state, nil
		}
	}

	ctx, cancel := ctx()
	defer cancel()

	var state BotState
	err := settingsColl.FindOne(ctx, bson.M{"_id": "global"}).Decode(&state)

	if err == mongo.ErrNoDocuments {
		s := newDefaultBotState()
		dbCache.Set(botStateCacheKey, s)
		return s, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to get bot state: %w", err)
	}

	buildIndexes(&state)
	dbCache.Set(botStateCacheKey, &state)

	return &state, nil
}

func updateBotState(newState *BotState) error {
	ctx, cancel := ctx()
	defer cancel()

	_, err := settingsColl.UpdateOne(
		ctx,
		bson.M{"_id": "global"},
		bson.M{"$set": newState},
		upsertOpt,
	)
	if err != nil {
		return fmt.Errorf("failed to update bot state: %w", err)
	}

	dbCache.Set(botStateCacheKey, newState)
	return nil
}

func modifyBotState(fn func(*BotState) bool) error {
	state, err := getBotState()
	if err != nil {
		return err
	}

	if fn(state) {
		return updateBotState(state)
	}

	return nil
}

func buildIndexes(s *BotState) {
	s.usersMap = make(map[int64]struct{}, len(s.Users))
	for _, userID := range s.Users {
		s.usersMap[userID] = struct{}{}
	}

	s.authorizedMap = make(map[int64]struct{}, len(s.AuthorizedUsers))
	for _, userID := range s.AuthorizedUsers {
		s.authorizedMap[userID] = struct{}{}
	}
}
