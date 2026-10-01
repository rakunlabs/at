package nativeauthtest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// FakeStore is an in-memory service.AuthStorer for handler tests.
type FakeStore struct {
	Mu       sync.Mutex
	Users    map[string]service.AuthUser
	Sessions map[string]service.AuthSession
	Claimed  bool
	Fail     bool
}

func (f *FakeStore) GetAuthUser(_ context.Context, name string) (*service.AuthUser, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Fail {
		return nil, errors.New("database unavailable")
	}
	u, ok := f.Users[name]
	if !ok {
		return nil, nil
	}
	return &u, nil
}

func (f *FakeStore) CreateAuthUser(_ context.Context, u service.AuthUser, bootstrap bool) (*service.AuthUser, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if _, ok := f.Users[u.Username]; ok || bootstrap && f.Claimed {
		return nil, service.ErrAuthConflict
	}
	if bootstrap {
		f.Claimed = true
		u.Admin = true
	}
	u.ID = u.Username
	f.Users[u.Username] = u
	return &u, nil
}

func (f *FakeStore) CreateAuthSession(_ context.Context, s service.AuthSession) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for _, u := range f.Users {
		if u.ID == s.UserID && !u.Disabled && u.SessionVersion == s.Version {
			f.Sessions[s.Hash] = s
			return nil
		}
	}
	return service.ErrAuthConflict
}

func (f *FakeStore) ResolveAuthSession(_ context.Context, hash string) (*service.AuthUser, time.Time, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Fail {
		return nil, time.Time{}, errors.New("database unavailable")
	}
	s, ok := f.Sessions[hash]
	if !ok {
		return nil, time.Time{}, nil
	}
	for _, u := range f.Users {
		if u.ID == s.UserID && !u.Disabled && u.SessionVersion == s.Version && s.ExpiresAt.After(time.Now()) {
			return &u, s.ExpiresAt, nil
		}
	}
	return nil, time.Time{}, nil
}

func (f *FakeStore) DeleteAuthSession(_ context.Context, hash string) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	delete(f.Sessions, hash)
	return nil
}

func (f *FakeStore) InvalidateAuthUser(_ context.Context, id string, disable bool) (bool, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for name, u := range f.Users {
		if u.ID == id {
			u.Disabled = u.Disabled || disable
			u.SessionVersion++
			f.Users[name] = u
			return true, nil
		}
	}
	return false, nil
}

func (f *FakeStore) ResolveAuthAccess(ctx context.Context, hash string) (*service.AuthUser, *service.AuthSession, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Fail {
		return nil, nil, errors.New("database unavailable")
	}
	for _, s := range f.Sessions {
		if s.AccessHash != hash || !s.AccessExpiresAt.After(time.Now()) || !s.ExpiresAt.After(time.Now()) {
			continue
		}
		for _, u := range f.Users {
			if u.ID == s.UserID && !u.Disabled && u.SessionVersion == s.Version {
				return &u, &s, nil
			}
		}
	}
	return nil, nil, nil
}

func (f *FakeStore) RotateAuthRefresh(ctx context.Context, hash, access, refresh string) (*service.AuthUser, *service.AuthSession, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Fail {
		return nil, nil, errors.New("database unavailable")
	}
	for id, s := range f.Sessions {
		if s.RefreshHash != hash || !s.ExpiresAt.After(time.Now()) {
			continue
		}
		for _, u := range f.Users {
			if u.ID == s.UserID && !u.Disabled && u.SessionVersion == s.Version {
				s.AccessHash = access
				s.RefreshHash = refresh
				s.AccessExpiresAt = time.Now().Add(10 * time.Minute)
				if s.AccessExpiresAt.After(s.ExpiresAt) {
					s.AccessExpiresAt = s.ExpiresAt
				}
				f.Sessions[id] = s
				return &u, &s, nil
			}
		}
	}
	return nil, nil, nil
}

func (f *FakeStore) RevokeAuthCredential(ctx context.Context, hash string) error {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Fail {
		return errors.New("database unavailable")
	}
	for id, s := range f.Sessions {
		if s.AccessHash == hash || s.RefreshHash == hash {
			delete(f.Sessions, id)
		}
	}
	return nil
}

func (f *FakeStore) CleanupAuthCredentials(ctx context.Context, limit uint) (int64, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var n int64
	for id, s := range f.Sessions {
		if n >= int64(limit) {
			break
		}
		if !s.ExpiresAt.After(time.Now()) {
			delete(f.Sessions, id)
			n++
		}
	}
	return n, nil
}

func (f *FakeStore) GetAuthUserByID(_ context.Context, id string) (*service.AuthUser, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Fail {
		return nil, errors.New("database unavailable")
	}
	for _, u := range f.Users {
		if u.ID == id {
			return &u, nil
		}
	}
	return nil, nil
}

func (f *FakeStore) ListAuthUsers(_ context.Context, q service.AuthUserQuery) ([]service.AuthUser, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Fail {
		return nil, errors.New("database unavailable")
	}
	var users []service.AuthUser
	for _, u := range f.Users {
		if u.ID <= q.After {
			continue
		}
		if q.Search != "" && !strings.Contains(strings.ToLower(u.Username), strings.ToLower(q.Search)) && !strings.Contains(strings.ToLower(u.ID), strings.ToLower(q.Search)) {
			continue
		}
		users = append(users, u)
	}
	slices.SortFunc(users, func(a, b service.AuthUser) int { return strings.Compare(a.ID, b.ID) })
	if len(users) > int(q.Limit) {
		users = users[:q.Limit]
	}
	return users, nil
}

func (f *FakeStore) DeleteAuthUser(_ context.Context, id string) (bool, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	if f.Fail {
		return false, errors.New("database unavailable")
	}
	for name, u := range f.Users {
		if u.ID != id {
			continue
		}
		if u.Admin && !u.Disabled {
			active := 0
			for _, other := range f.Users {
				if other.Admin && !other.Disabled {
					active++
				}
			}
			if active <= 1 {
				return false, service.ErrAuthConflict
			}
		}
		delete(f.Users, name)
		for hash, s := range f.Sessions {
			if s.UserID == id {
				delete(f.Sessions, hash)
			}
		}
		return true, nil
	}
	return false, nil
}

func (f *FakeStore) EnableAuthUser(_ context.Context, id string) (bool, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for name, u := range f.Users {
		if u.ID == id {
			u.Disabled = false
			u.SessionVersion++
			f.Users[name] = u
			return true, nil
		}
	}
	return false, nil
}

func (f *FakeStore) SetAuthUserPassword(_ context.Context, id, hash string, version *int64) (bool, error) {
	f.Mu.Lock()
	defer f.Mu.Unlock()
	for name, u := range f.Users {
		if u.ID == id {
			if version != nil && (u.SessionVersion != *version || u.Disabled) {
				return false, service.ErrAuthConflict
			}
			u.PasswordHash = hash
			u.SessionVersion++
			f.Users[name] = u
			for hash, s := range f.Sessions {
				if s.UserID == id {
					delete(f.Sessions, hash)
				}
			}
			return true, nil
		}
	}
	if version != nil {
		return false, service.ErrAuthConflict
	}
	return false, nil
}
