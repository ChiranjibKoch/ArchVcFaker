package utils

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tgmultibot/config"

	tgconv "github.com/mtgo-labs/session-converter"
	_ "modernc.org/sqlite"
)

// SessionResult holds the outcome of converting one session file.
type SessionResult struct {
	File    string // original base name (without .session)
	Session string // gogram-encoded string session on success
	Err     error  // non-nil on failure
}

// ConvertSessionFiles converts multiple raw Telethon .session SQLite payloads
// directly in Go and returns gogram-encoded string sessions.
func ConvertSessionFiles(sessions []struct {
	Name string
	Data []byte
}) []SessionResult {
	if len(sessions) == 0 {
		return nil
	}

	tmpDir, err := os.MkdirTemp("", "tgsess_batch_*")
	if err != nil {
		out := make([]SessionResult, len(sessions))
		for i, s := range sessions {
			out[i] = SessionResult{File: s.Name, Err: fmt.Errorf("mktemp: %w", err)}
		}
		return out
	}
	defer os.RemoveAll(tmpDir)

	results := make([]SessionResult, len(sessions))
	for i, s := range sessions {
		results[i].File = s.Name

		path := filepath.Join(tmpDir, s.Name+".session")
		if err := os.WriteFile(path, s.Data, 0600); err != nil {
			results[i].Err = fmt.Errorf("write session file: %w", err)
			continue
		}

		converted, err := ConvertSessionSQLiteFile(path)
		if err != nil {
			results[i].Err = err
			continue
		}
		results[i].Session = converted
	}

	return results
}

// ConvertSessionFile is a convenience wrapper that converts a single session
// using the batch path.
func ConvertSessionFile(sessionData []byte, baseName string) (string, error) {
	res := ConvertSessionFiles([]struct {
		Name string
		Data []byte
	}{{Name: baseName, Data: sessionData}})
	if len(res) == 0 {
		return "", fmt.Errorf("no result returned")
	}
	if res[0].Err != nil {
		return "", res[0].Err
	}
	return res[0].Session, nil
}

// ConvertSessionSQLiteFile reads a Telethon SQLite session file and exports it
// as a gogram string session using mtgo-labs/session-converter.
func ConvertSessionSQLiteFile(path string) (string, error) {
	session, err := readTelethonSQLite(path)
	if err != nil {
		return "", err
	}

	session.AppID = int32(config.ApiID)
	session.FillDefaults()

	encoded, err := tgconv.Encode(session, tgconv.FormatGogram)
	if err != nil {
		return "", fmt.Errorf("encode gogram session: %w", err)
	}
	return encoded, nil
}

func readTelethonSQLite(path string) (*tgconv.Session, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite session: %w", err)
	}
	defer db.Close()

	var dcID int
	var serverAddress string
	var port int
	var authKey []byte
	row := db.QueryRow(`SELECT dc_id, server_address, port, auth_key FROM sessions LIMIT 1`)
	if err := row.Scan(&dcID, &serverAddress, &port, &authKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("telethon session has no rows")
		}
		return nil, fmt.Errorf("read telethon session: %w", err)
	}

	serverAddress = strings.TrimSpace(serverAddress)
	if len(authKey) != 256 {
		return nil, fmt.Errorf("invalid auth key length: got %d, want 256", len(authKey))
	}
	if dcID <= 0 {
		return nil, fmt.Errorf("invalid dc id: %d", dcID)
	}
	if serverAddress == "" {
		return nil, fmt.Errorf("empty server address")
	}
	if port <= 0 {
		return nil, fmt.Errorf("invalid port: %d", port)
	}

	return &tgconv.Session{
		DCID:          dcID,
		ServerAddress: serverAddress,
		Port:          port,
		AuthKey:       authKey,
	}, nil
}
