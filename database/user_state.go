package database

import (
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// ClientInfo stores one managed Telegram account.
//
// Phone and EncodedSession are stored encrypted in MongoDB (see crypto.go).
// Always use Phone() / Session() to read, and SetPhone() / SetSession() to
// write — these handle encrypt/decrypt transparently. The raw bson fields
// (ph, es) hold ciphertext and should never be accessed directly outside
// this file.
type ClientInfo struct {
	AccountID      int64  `bson:"aid"`
	phone          string `bson:"-"`
	session        string `bson:"-"`
	PhoneEnc       string `bson:"ph,omitempty"`
	SessionEnc     string `bson:"es"`
	Disabled       bool   `bson:"dis,omitempty"`
	DisabledReason string `bson:"dre,omitempty"`
}

// Phone returns the decrypted phone number.
func (c *ClientInfo) Phone() string { return c.phone }

// Session returns the decrypted session string.
func (c *ClientInfo) Session() string { return c.session }

// SetPhone encrypts and stores the phone number.
func (c *ClientInfo) SetPhone(plain string) error {
	enc, err := encryptStr(plain)
	if err != nil {
		return fmt.Errorf("SetPhone (account %d): %w", c.AccountID, err)
	}
	c.phone = plain
	c.PhoneEnc = enc
	return nil
}

// SetSession encrypts and stores the session string.
func (c *ClientInfo) SetSession(plain string) error {
	enc, err := encryptStr(plain)
	if err != nil {
		return fmt.Errorf("SetSession (account %d): %w", c.AccountID, err)
	}
	c.session = plain
	c.SessionEnc = enc
	return nil
}

// decode populates the unexported plain fields by decrypting the stored
// ciphertext. Called automatically after loading from MongoDB.
func (c *ClientInfo) decode() error {
	phone, err := decryptStr(c.PhoneEnc)
	if err != nil {
		return fmt.Errorf("decode phone (account %d): %w", c.AccountID, err)
	}
	session, err := decryptStr(c.SessionEnc)
	if err != nil {
		return fmt.Errorf("decode session (account %d): %w", c.AccountID, err)
	}
	c.phone = phone
	c.session = session
	return nil
}

// UserState stores all user-specific data.
type UserState struct {
	UserID         int64        `bson:"_id"`
	Clients        []ClientInfo `bson:"cli,omitempty"`
	AutoReactChats []int64      `bson:"arc,omitempty"`
	AutoViewChats  []int64      `bson:"avc,omitempty"`
	AccessToken    string       `bson:"tok,omitempty"`
	Delegates      []int64      `bson:"dlg,omitempty"`

	clientIdx map[int64]int `bson:"-"`
}

func defaultUserState(userID int64) *UserState {
	s := &UserState{
		UserID:         userID,
		Clients:        []ClientInfo{},
		AutoReactChats: []int64{},
		AutoViewChats:  []int64{},
		Delegates:      []int64{},
	}
	buildClientIndex(s)
	return s
}

func buildClientIndex(s *UserState) {
	s.clientIdx = make(map[int64]int, len(s.Clients))
	for i, c := range s.Clients {
		s.clientIdx[c.AccountID] = i
	}
}

// decodeClients decrypts all ClientInfo entries after loading from MongoDB.
func decodeClients(s *UserState) error {
	for i := range s.Clients {
		if err := s.Clients[i].decode(); err != nil {
			return err
		}
	}
	return nil
}

func getUserState(userID int64) (*UserState, error) {
	if state, found := userStateCache.Get(userID); found {
		return state, nil
	}

	ctx, cancel := ctx()
	defer cancel()

	var state UserState
	err := userStateColl.FindOne(ctx, bson.M{"_id": userID}).Decode(&state)

	if err == mongo.ErrNoDocuments {
		def := defaultUserState(userID)
		userStateCache.Set(userID, def)
		return def, nil
	}
	if err != nil {
		return nil, fmt.Errorf("getUserState(%d): %w", userID, err)
	}

	if err := decodeClients(&state); err != nil {
		return nil, fmt.Errorf("getUserState(%d): %w", userID, err)
	}
	buildClientIndex(&state)
	userStateCache.Set(userID, &state)
	return &state, nil
}

func updateUserState(state *UserState) error {
	ctx, cancel := ctx()
	defer cancel()

	setFields := bson.M{"$set": state}
	unsetFields := bson.M{}

	if len(state.AutoReactChats) == 0 {
		unsetFields["arc"] = ""
	}
	if len(state.AutoViewChats) == 0 {
		unsetFields["avc"] = ""
	}

	update := bson.M{}
	for k, v := range setFields {
		update[k] = v
	}
	if len(unsetFields) > 0 {
		update["$unset"] = unsetFields
	}

	_, err := userStateColl.UpdateOne(
		ctx,
		bson.M{"_id": state.UserID},
		update,
		upsertOpt,
	)
	if err != nil {
		return fmt.Errorf("updateUserState(%d): %w", state.UserID, err)
	}

	userStateCache.Set(state.UserID, state)
	return nil
}

func modifyUserState(userID int64, fn func(*UserState) bool) error {
	state, err := getUserState(userID)
	if err != nil {
		return err
	}
	if fn(state) {
		return updateUserState(state)
	}
	return nil
}
