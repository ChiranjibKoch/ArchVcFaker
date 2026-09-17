package database

import (
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"strings"
)

// generateTokenString returns a 4-4-4-4 base32 token, e.g. "K3F9-QXJ2-7H4P-MN8R".
func generateTokenString() (string, error) {
	raw := make([]byte, 13) // ceil(16 * 5 / 8) = 10 for 16 chars; use 13 for 4-4-4-4 = 16 base32 chars
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generateTokenString: %w", err)
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	enc = strings.ToUpper(enc)
	if len(enc) > 16 {
		enc = enc[:16]
	}

	// format as XXXX-XXXX-XXXX-XXXX
	var b strings.Builder
	for i, ch := range enc {
		if i != 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteRune(ch)
	}
	return b.String(), nil
}

// GetOrCreateAccessToken returns the user's permanent access token,
// generating one if they don't have one yet.
func GetOrCreateAccessToken(userID int64) (string, error) {
	state, err := getUserState(userID)
	if err != nil {
		return "", err
	}
	if state.AccessToken != "" {
		return state.AccessToken, nil
	}

	token, err := generateTokenString()
	if err != nil {
		return "", err
	}

	var result string
	err = modifyUserState(userID, func(s *UserState) bool {
		if s.AccessToken != "" {
			result = s.AccessToken
			return false
		}
		s.AccessToken = token
		result = token
		return true
	})
	return result, err
}

// RegenerateAccessToken replaces the user's token with a fresh one.
// Existing delegates keep their access — only the shareable token changes.
func RegenerateAccessToken(userID int64) (string, error) {
	token, err := generateTokenString()
	if err != nil {
		return "", err
	}
	err = modifyUserState(userID, func(s *UserState) bool {
		s.AccessToken = token
		return true
	})
	return token, err
}

// FindAccessTokenOwner searches for the user who owns the given token.
// ok is false if no user has this token.
func FindAccessTokenOwner(token string) (grantorID int64, ok bool, err error) {
	ctx, cancel := ctx()
	defer cancel()

	cur, ferr := userStateColl.Find(ctx, map[string]any{"tok": token})
	if ferr != nil {
		return 0, false, fmt.Errorf("FindAccessTokenOwner: %w", ferr)
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		var s UserState
		if derr := cur.Decode(&s); derr != nil {
			continue
		}
		return s.UserID, true, nil
	}
	return 0, false, cur.Err()
}

// ---- Delegations ------------------------------------------------------------

func AddDelegate(grantorID, delegateID int64) error {
	err := modifyUserState(grantorID, func(s *UserState) bool {
		var added bool
		s.Delegates, added = addUnique(s.Delegates, delegateID)
		return added
	})
	if err == nil {
		delegateGrantorsMu.Lock()
		delegateGrantors[delegateID], _ = addUnique(delegateGrantors[delegateID], grantorID)
		delegateGrantorsMu.Unlock()
	}
	return err
}

func RemoveDelegate(grantorID, delegateID int64) error {
	err := modifyUserState(grantorID, func(s *UserState) bool {
		var removed bool
		s.Delegates, removed = removeElement(s.Delegates, delegateID)
		return removed
	})
	if err == nil {
		delegateGrantorsMu.Lock()
		delegateGrantors[delegateID], _ = removeElement(delegateGrantors[delegateID], grantorID)
		if len(delegateGrantors[delegateID]) == 0 {
			delete(delegateGrantors, delegateID)
		}
		delegateGrantorsMu.Unlock()
	}
	return err
}

func ListDelegates(grantorID int64) ([]int64, error) {
	state, err := getUserState(grantorID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(state.Delegates))
	copy(out, state.Delegates)
	return out, nil
}

func GrantorsFor(userID int64) []int64 {
	delegateGrantorsMu.RLock()
	defer delegateGrantorsMu.RUnlock()
	out := make([]int64, len(delegateGrantors[userID]))
	copy(out, delegateGrantors[userID])
	return out
}

func IsDelegate(userID int64) bool {
	delegateGrantorsMu.RLock()
	defer delegateGrantorsMu.RUnlock()
	return len(delegateGrantors[userID]) > 0
}
