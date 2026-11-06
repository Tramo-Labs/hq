package ledger

import (
	"context"
	"sync"
)

// MemStore is the in-memory Store used in tests and as the reference
// behavior for the Postgres store: files unique per tenant, movements unique
// per account and key, quarantined files kept apart.
type MemStore struct {
	mu          sync.Mutex
	files       map[string]bool                // tenant|hash
	movements   map[string]map[string]Movement // tenant|account -> key -> movement
	statements  map[string][]StatementSummary  // tenant|account
	Quarantine  []Result
	AcceptedLog []Result
}

// NewMemStore returns an empty store.
func NewMemStore() *MemStore {
	return &MemStore{
		files:      map[string]bool{},
		movements:  map[string]map[string]Movement{},
		statements: map[string][]StatementSummary{},
	}
}

func (s *MemStore) FileSeen(_ context.Context, tenant, hash string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.files[tenant+"|"+hash], nil
}

func (s *MemStore) MovementsSeen(_ context.Context, tenant, account string, keys []string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for _, k := range keys {
		if _, ok := s.movements[tenant+"|"+account][k]; ok {
			out[k] = true
		}
	}
	return out, nil
}

func (s *MemStore) LastStatement(_ context.Context, tenant, account string) (*StatementSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.statements[tenant+"|"+account]
	var last *StatementSummary
	for i := range list {
		if last == nil || list[i].To.After(last.To) {
			last = &list[i]
		}
	}
	return last, nil
}

func (s *MemStore) SaveAccepted(_ context.Context, u Upload, r Result, fresh []Movement) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	acct := u.TenantID + "|" + u.AccountID
	s.files[u.TenantID+"|"+r.FileHash] = true
	if s.movements[acct] == nil {
		s.movements[acct] = map[string]Movement{}
	}
	for _, m := range fresh {
		s.movements[acct][m.Key] = m
	}
	st := r.Statement
	s.statements[acct] = append(s.statements[acct], StatementSummary{From: st.From, To: st.To, Closing: st.Closing})
	s.AcceptedLog = append(s.AcceptedLog, r)
	return nil
}

// SaveQuarantined keeps the result. The file hash is not marked as seen: once
// a layout is fixed the same file can be uploaded again and enter.
func (s *MemStore) SaveQuarantined(_ context.Context, u Upload, r Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Quarantine = append(s.Quarantine, r)
	return nil
}

// Movements returns the stored movements of an account (unordered).
func (s *MemStore) Movements(tenant, account string) []Movement {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Movement
	for _, m := range s.movements[tenant+"|"+account] {
		out = append(out, m)
	}
	return out
}
