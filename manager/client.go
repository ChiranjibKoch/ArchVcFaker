package manager

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"tgmultibot/database"
	"tgmultibot/ntgcalls"
	"tgmultibot/ubot"

	"github.com/amarnathcjd/gogram/telegram"
)

type ManagedClient struct {
	Client    *telegram.Client
	AccountID int64
	OwnerID   int64
}

// sharedAccount holds the single real connection for one underlying Telegram
// account. Multiple owners may reference the same sharedAccount (e.g. the
// same session uploaded by, or shared with, more than one bot user) — in
// that case only ONE telegram.Client is ever connected for that account,
// and refCount tracks how many owners currently reference it.
type sharedAccount struct {
	client    *telegram.Client
	accountID int64
	refCount  int
}

type ClientManager struct {
	mu       sync.RWMutex
	clients  map[int64][]*ManagedClient // ownerID -> attached clients (view)
	accounts map[int64]*sharedAccount   // accountID -> the single real connection

	ubotsMu sync.Mutex
	ubots   map[*telegram.Client]*ubot.Context // client -> ntgcalls userbot instance

	endHookMu sync.RWMutex
	endHook   func(ownerID int64, chatIDNum int64)

	appID   int32
	appHash string
}

// SetOnPlaybackEnd registers a callback fired when a stream reaches its end and
// no further loop is pending. The media file is kept for a short grace period
// so the owner can still replay it.
func (m *ClientManager) SetOnPlaybackEnd(fn func(ownerID int64, chatIDNum int64)) {
	m.endHookMu.Lock()
	m.endHook = fn
	m.endHookMu.Unlock()
}

func (m *ClientManager) notifyPlaybackEnd(ownerID, chatIDNum int64) {
	m.endHookMu.RLock()
	fn := m.endHook
	m.endHookMu.RUnlock()
	if fn != nil {
		fn(ownerID, chatIDNum)
	}
}

func NewClientManager(appID int32, appHash string) *ClientManager {
	return &ClientManager{
		clients:  make(map[int64][]*ManagedClient),
		accounts: make(map[int64]*sharedAccount),
		ubots:    make(map[*telegram.Client]*ubot.Context),
		appID:    appID,
		appHash:  appHash,
	}
}

// getUbot returns (creating if needed) the ntgcalls userbot instance bound to
// the given live client. One instance is shared per underlying account so the
// raw update handlers are only ever registered once per client.
func (m *ClientManager) getUbot(client *telegram.Client) (*ubot.Context, error) {
	m.ubotsMu.Lock()
	if u, ok := m.ubots[client]; ok {
		m.ubotsMu.Unlock()
		return u, nil
	}
	m.ubotsMu.Unlock()

	u, err := ubot.NewInstance(client)
	if err != nil {
		return nil, err
	}

	u.OnStreamEnd(func(chatIDNum int64, _ ntgcalls.StreamType, _ ntgcalls.StreamDevice) {
		var targets []*VoiceChatSession
		vcManager.mu.RLock()
		for _, sess := range vcManager.sessions {
			if sess.Ubot == u && sess.ChatIDNum == chatIDNum {
				targets = append(targets, sess)
			}
		}
		vcManager.mu.RUnlock()
		for _, sess := range targets {
			notify := func() { m.notifyPlaybackEnd(sess.OwnerID, sess.ChatIDNum) }
			sess.handleStreamEnd(notify)
		}
	})

	m.ubotsMu.Lock()
	defer m.ubotsMu.Unlock()
	if existing, ok := m.ubots[client]; ok {
		u.Close()
		return existing, nil
	}
	m.ubots[client] = u
	return u, nil
}

func (m *ClientManager) RestoreFromDB(logFn func(msg string)) {
	states, err := database.AllUsersWithClients()
	if err != nil {
		log.Printf("RestoreFromDB: failed to list users: %v", err)
		return
	}

	// Group every (owner, ClientInfo) pair by the underlying AccountID first.
	// If the same Telegram account is stored under several owners, we must
	// connect it exactly once — connecting it once per owner would fire
	// Connect()/GetMe() repeatedly for the SAME account simultaneously and
	// trigger FLOOD_WAIT.
	type ownerSession struct {
		ownerID int64
		ci      database.ClientInfo
	}
	groups := make(map[int64][]ownerSession)

	for _, state := range states {
		for _, ci := range state.Clients {
			if ci.Disabled {
				continue
			}
			groups[ci.AccountID] = append(groups[ci.AccountID], ownerSession{state.UserID, ci})
		}
	}

	var wg sync.WaitGroup
	for accountID, owners := range groups {
		wg.Add(1)
		go func(accountID int64, owners []ownerSession) {
			defer wg.Done()

			var (
				mc           *ManagedClient
				lastErr      error
				connectedVia int64 = -1
			)

			// Try each owner's stored session for this account in turn until
			// one connects — a single corrupted/stale session shouldn't block
			// the rest if another owner's copy for the same account works.
			for _, o := range owners {
				got, cerr := m.addClientShared(o.ownerID, o.ci.Session(), accountID)
				if cerr != nil {
					lastErr = cerr
					continue
				}
				mc = got
				connectedVia = o.ownerID
				break
			}

			if mc == nil {
				reason := classifySessionError(lastErr)
				for _, o := range owners {
					if reason != "" {
						_ = database.DisableUserClient(o.ownerID, o.ci.AccountID, reason)
						if logFn != nil {
							logFn(fmt.Sprintf(
								"⚠️ <b>Startup:</b> account <code>%d</code> (owner <code>%d</code>) disabled — <b>%s</b>\n<pre>%s</pre>",
								o.ci.AccountID,
								o.ownerID,
								reason,
								lastErr.Error(),
							))
						}
					} else {
						log.Printf("RestoreFromDB: connect acc %d (owner %d): %v", accountID, o.ownerID, lastErr)
						if logFn != nil {
							logFn(fmt.Sprintf(
								"⚠️ <b>Startup:</b> account <code>%d</code> (owner <code>%d</code>) failed to connect — <pre>%s</pre>",
								o.ci.AccountID, o.ownerID, lastErr.Error(),
							))
						}
					}
				}
				return
			}

			// Attach the already-connected client to every other owner that
			// also has this account — no extra Connect()/GetMe() involved.
			for _, o := range owners {
				if o.ownerID == connectedVia {
					continue
				}
				if _, err := m.addClientShared(o.ownerID, "", accountID); err != nil {
					log.Printf("RestoreFromDB: attach acc %d to owner %d: %v", accountID, o.ownerID, err)
				}
			}
		}(accountID, owners)
	}
	wg.Wait()
	log.Printf(
		"RestoreFromDB: done — %d unique account(s) connected, %d total attachment(s)",
		m.UniqueAccountCount(), m.TotalCount(),
	)
}

// connectAccount opens a brand-new real connection (Connect + GetMe) for a
// session string. Used only when no existing sharedAccount can be reused.
func (m *ClientManager) connectAccount(stringSession string) (*telegram.Client, *telegram.UserObj, error) {
	client, err := telegram.NewClient(telegram.ClientConfig{
		AppID:         m.appID,
		AppHash:       m.appHash,
		StringSession: stringSession,
		MemorySession: true,
	})
	if err != nil {
		return nil, nil, err
	}

	if err := client.Connect(); err != nil {
		return nil, nil, err
	}

	me, err := getMeWithFloodWait(client)
	if err != nil {
		client.Stop()
		return nil, nil, err
	}

	client.On(telegram.OnMessage, m.automationHandler)

	return client, me, nil
}

// addClientShared attaches accountID to ownerID, reusing an existing live
// connection for that account if one already exists. knownAccountID may be
// 0 when the caller doesn't yet know which account a session belongs to
// (e.g. a fresh manual add) — in that case it always connects first and
// only then checks for an existing connection to reconcile against.
func (m *ClientManager) addClientShared(
	ownerID int64,
	stringSession string,
	knownAccountID int64,
) (*ManagedClient, error) {
	if knownAccountID != 0 {
		m.mu.Lock()
		if sa, ok := m.accounts[knownAccountID]; ok {
			sa.refCount++
			mc := m.attachOwnerLocked(ownerID, sa)
			m.mu.Unlock()
			return mc, nil
		}
		m.mu.Unlock()
	}

	client, me, err := m.connectAccount(stringSession)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Someone else may have connected this very account concurrently
	// (e.g. two owners adding it at almost the same instant) — reuse the
	// winner and discard this redundant connection instead of running two
	// live connections for the same account.
	if sa, ok := m.accounts[me.ID]; ok {
		client.Stop()
		sa.refCount++
		return m.attachOwnerLocked(ownerID, sa), nil
	}

	sa := &sharedAccount{client: client, accountID: me.ID, refCount: 1}
	m.accounts[me.ID] = sa
	return m.attachOwnerLocked(ownerID, sa), nil
}

// attachOwnerLocked records (or replaces) ownerID's reference to sa.
// Caller must hold m.mu. If ownerID already referenced this account, the
// caller's prior refCount++ is reverted since no new reference was created.
func (m *ClientManager) attachOwnerLocked(ownerID int64, sa *sharedAccount) *ManagedClient {
	mc := &ManagedClient{Client: sa.client, AccountID: sa.accountID, OwnerID: ownerID}
	for i, existing := range m.clients[ownerID] {
		if existing.AccountID == sa.accountID {
			m.clients[ownerID][i] = mc
			sa.refCount--
			return mc
		}
	}
	m.clients[ownerID] = append(m.clients[ownerID], mc)
	return mc
}

// detachLocked removes ownerID's reference to accountID and releases the
// underlying connection (Stop()) only once no owner references it anymore.
// Caller must hold m.mu.
func (m *ClientManager) detachLocked(ownerID, accountID int64) {
	clients := m.clients[ownerID]
	for i, mc := range clients {
		if mc.AccountID == accountID {
			m.clients[ownerID] = append(clients[:i], clients[i+1:]...)
			if len(m.clients[ownerID]) == 0 {
				delete(m.clients, ownerID)
			}
			break
		}
	}

	sa, ok := m.accounts[accountID]
	if !ok {
		return
	}
	sa.refCount--
	if sa.refCount <= 0 {
		sa.client.Stop()
		delete(m.accounts, accountID)
		m.ubotsMu.Lock()
		if u, ok := m.ubots[sa.client]; ok {
			delete(m.ubots, sa.client)
			u.Close()
		}
		m.ubotsMu.Unlock()
	}
}

// getMeWithFloodWait calls GetMe and retries once if a FLOOD_WAIT is returned.
func getMeWithFloodWait(client *telegram.Client) (*telegram.UserObj, error) {
	me, err := client.GetMe()
	if err == nil {
		return me, nil
	}

	wait := telegram.GetFloodWait(err)
	if wait <= 0 {
		return nil, err
	}

	log.Printf("getMeWithFloodWait: flood wait %d seconds, retrying...", wait)
	time.Sleep(time.Duration(wait+1) * time.Second)

	me, err = client.GetMe()
	if err != nil {
		return nil, err
	}
	return me, nil
}

func (m *ClientManager) AddClient(ownerID int64, stringSession string) (*ManagedClient, error) {
	mc, err := m.addClientShared(ownerID, stringSession, 0)
	if err != nil {
		return nil, err
	}

	me := mc.Client.Me()
	phone := ""
	if me != nil {
		phone = me.Phone
	}
	ci := database.ClientInfo{AccountID: mc.AccountID}
	if err := ci.SetPhone(phone); err != nil {
		log.Printf("AddClient: encrypt phone user %d acc %d: %v", ownerID, mc.AccountID, err)
	}
	if err := ci.SetSession(stringSession); err != nil {
		log.Printf("AddClient: encrypt session user %d acc %d: %v", ownerID, mc.AccountID, err)
	}
	dbErr := database.UpsertUserClient(ownerID, ci)
	if dbErr != nil {
		log.Printf("AddClient: persist user %d acc %d: %v", ownerID, mc.AccountID, dbErr)
	}

	return mc, nil
}

func (m *ClientManager) AddClients(ownerID int64, sessions []string) ([]*ManagedClient, []error) {
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		added []*ManagedClient
		errs  []error
	)

	for _, sess := range sessions {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			mc, err := m.AddClient(ownerID, s)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				added = append(added, mc)
			}
		}(sess)
	}

	wg.Wait()
	return added, errs
}

func (m *ClientManager) DisableClient(ownerID, accountID int64, reason string) {
	m.mu.Lock()
	m.detachLocked(ownerID, accountID)
	m.mu.Unlock()

	if err := database.DisableUserClient(ownerID, accountID, reason); err != nil {
		log.Printf("DisableClient: DB update user %d acc %d: %v", ownerID, accountID, err)
	}
}

func (m *ClientManager) GetClientByID(ownerID, accountID int64) *ManagedClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, mc := range m.clients[ownerID] {
		if mc.AccountID == accountID {
			return mc
		}
	}
	return nil
}

func (m *ClientManager) GetClients(ownerID int64) []*ManagedClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[ownerID]
}

func (m *ClientManager) GetAllClients() []*ManagedClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []*ManagedClient
	for _, clients := range m.clients {
		all = append(all, clients...)
	}
	return all
}

func (m *ClientManager) OwnerIDs() []int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]int64, 0, len(m.clients))
	for id := range m.clients {
		ids = append(ids, id)
	}
	return ids
}

func (m *ClientManager) RemoveClient(ownerID int64, index int) error {
	m.mu.Lock()
	clients, ok := m.clients[ownerID]
	if !ok || len(clients) == 0 {
		m.mu.Unlock()
		return fmt.Errorf("no clients found for ownerID %d", ownerID)
	}
	if index < 0 || index >= len(clients) {
		m.mu.Unlock()
		return fmt.Errorf("index %d out of range for ownerID %d (total: %d)", index, ownerID, len(clients))
	}

	accountID := clients[index].AccountID
	m.detachLocked(ownerID, accountID)
	m.mu.Unlock()

	if err := database.RemoveUserClient(ownerID, accountID); err != nil {
		log.Printf("RemoveClient: DB remove user %d acc %d: %v", ownerID, accountID, err)
	}
	return nil
}

func (m *ClientManager) RemoveAllClients(ownerID int64) error {
	m.mu.Lock()
	clients, ok := m.clients[ownerID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("no clients found for ownerID %d", ownerID)
	}
	accountIDs := make([]int64, len(clients))
	for i, mc := range clients {
		accountIDs[i] = mc.AccountID
	}
	for _, aid := range accountIDs {
		m.detachLocked(ownerID, aid)
	}
	m.mu.Unlock()

	for _, aid := range accountIDs {
		if err := database.RemoveUserClient(ownerID, aid); err != nil {
			log.Printf("RemoveAllClients: DB remove user %d acc %d: %v", ownerID, aid, err)
		}
	}
	return nil
}

func (m *ClientManager) DoAll(ownerID int64, action func(*ManagedClient) error) []error {
	m.mu.RLock()
	clients, ok := m.clients[ownerID]
	m.mu.RUnlock()

	if !ok || len(clients) == 0 {
		return []error{fmt.Errorf("no clients found for ownerID %d", ownerID)}
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, mc := range clients {
		wg.Add(1)
		go func(c *ManagedClient) {
			defer wg.Done()
			if err := action(c); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(mc)
	}

	wg.Wait()
	return errs
}

func (m *ClientManager) DoAllGlobal(action func(*ManagedClient) error) []error {
	all := m.GetAllClients()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, mc := range all {
		wg.Add(1)
		go func(c *ManagedClient) {
			defer wg.Done()
			if err := action(c); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(mc)
	}

	wg.Wait()
	return errs
}

func (m *ClientManager) Count(ownerID int64) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.clients[ownerID])
}

func (m *ClientManager) TotalCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, c := range m.clients {
		total += len(c)
	}
	return total
}

// UniqueAccountCount returns the number of distinct live Telegram account
// connections currently held — as opposed to TotalCount, which counts every
// owner-attachment (a shared account attached to 3 owners counts as 1 here,
// 3 there).
func (m *ClientManager) UniqueAccountCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.accounts)
}

func (m *ClientManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ubotsMu.Lock()
	for client, u := range m.ubots {
		u.Close()
		delete(m.ubots, client)
	}
	m.ubotsMu.Unlock()
	for _, sa := range m.accounts {
		sa.client.Stop()
	}
	m.clients = make(map[int64][]*ManagedClient)
	m.accounts = make(map[int64]*sharedAccount)
}

func classifySessionError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToUpper(err.Error())

	fatalPhrases := []struct {
		contains string
		reason   string
	}{
		{"AUTH_KEY_UNREGISTERED", "SESSION_REVOKED"},
		{"AUTH_KEY_INVALID", "SESSION_INVALID"},
		{"SESSION_REVOKED", "SESSION_REVOKED"},
		{"SESSION_EXPIRED", "SESSION_EXPIRED"},
		{"USER_DEACTIVATED", "ACCOUNT_BANNED"},
		{"USER_DEACTIVATED_BAN", "ACCOUNT_BANNED"},
		{"ACCOUNT_BANNED", "ACCOUNT_BANNED"},
		{"PHONE_NUMBER_BANNED", "PHONE_BANNED"},
		{"AUTH_KEY_DUPLICATED", "SESSION_DUPLICATE"},
		{"FROZEN", "ACCOUNT_FROZEN"},
	}

	for _, p := range fatalPhrases {
		if strings.Contains(msg, p.contains) {
			return p.reason
		}
	}
	return ""
}
