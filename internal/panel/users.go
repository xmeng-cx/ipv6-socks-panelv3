package panel

import (
	"context"
	"crypto/subtle"
	"fmt"
	"sort"
	"strings"
)

func (m *Manager) ensureAuthState() error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	changed := m.state.Version != stateVersion
	m.state.Version = stateVersion
	if m.state.SessionSecret == "" {
		secret, err := randomSecret()
		if err != nil {
			return err
		}
		m.state.SessionSecret = secret
		changed = true
	}
	if len(m.state.Users) == 0 {
		admin, err := newUser(m.cfg.AdminUsername, m.cfg.AdminPassword, "admin")
		if err != nil {
			return fmt.Errorf("create initial administrator: %w", err)
		}
		m.state.Users = []User{admin}
		changed = true
	}
	admin, ok := m.userLocked(m.cfg.AdminUsername)
	if !ok {
		return fmt.Errorf("configured administrator %q is missing", m.cfg.AdminUsername)
	}
	for i := range m.state.Proxies {
		if m.state.Proxies[i].Owner == "" {
			m.state.Proxies[i].Owner = admin.Username
			changed = true
		}
		owner, exists := m.userLocked(m.state.Proxies[i].Owner)
		if !exists {
			return fmt.Errorf("proxy %d belongs to missing user %q", m.state.Proxies[i].Port, m.state.Proxies[i].Owner)
		}
		if m.state.Proxies[i].Username == "" {
			m.state.Proxies[i].Username = owner.Username
			changed = true
		}
		if m.state.Proxies[i].Password == "" {
			m.state.Proxies[i].Password = owner.ProxyPassword
			changed = true
		}
	}
	if changed {
		return m.store.Save(m.state)
	}
	return nil
}

func (m *Manager) userLocked(username string) (User, bool) {
	for _, user := range m.state.Users {
		if subtle.ConstantTimeCompare([]byte(user.Username), []byte(username)) == 1 {
			return user, true
		}
	}
	return User{}, false
}

func (m *Manager) User(username string) (User, bool) {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.userLocked(username)
}

func (m *Manager) Authenticate(username, password string) (User, bool) {
	m.stateMu.RLock()
	user, ok := m.userLocked(strings.TrimSpace(username))
	m.stateMu.RUnlock()
	return user, ok && verifyPassword(user, password)
}

func (m *Manager) SessionSecret() string {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	return m.state.SessionSecret
}

func (m *Manager) Users() []UserView {
	m.stateMu.RLock()
	defer m.stateMu.RUnlock()
	counts := map[string]int{}
	for _, proxy := range m.state.Proxies {
		counts[proxy.Owner]++
	}
	result := make([]UserView, 0, len(m.state.Users))
	for _, user := range m.state.Users {
		result = append(result, UserView{Username: user.Username, Role: user.Role, ProxyCount: counts[user.Username], CreatedAt: user.CreatedAt})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Role != result[j].Role {
			return result[i].Role == "admin"
		}
		return result[i].Username < result[j].Username
	})
	return result
}

func (m *Manager) AddUser(username, password string) (UserView, error) {
	user, err := newUser(username, password, "user")
	if err != nil {
		return UserView{}, err
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	if _, exists := m.userLocked(user.Username); exists {
		return UserView{}, ErrUserExists
	}
	m.state.Users = append(m.state.Users, user)
	if err := m.store.Save(m.state); err != nil {
		m.state.Users = m.state.Users[:len(m.state.Users)-1]
		return UserView{}, err
	}
	return UserView{Username: user.Username, Role: user.Role, CreatedAt: user.CreatedAt}, nil
}

func (m *Manager) DeleteUser(ctx context.Context, username string) error {
	user, ok := m.User(username)
	if !ok {
		return ErrNotFound
	}
	if user.Role == "admin" {
		return ErrForbidden
	}
	for _, proxy := range m.ListForUser(username) {
		if err := m.Delete(ctx, proxy.ID); err != nil {
			return fmt.Errorf("delete user proxy %d: %w", proxy.Port, err)
		}
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	for i := range m.state.Users {
		if m.state.Users[i].Username == username {
			old := m.state.Users[i]
			m.state.Users = append(m.state.Users[:i], m.state.Users[i+1:]...)
			if err := m.store.Save(m.state); err != nil {
				m.state.Users = append(m.state.Users, old)
				return err
			}
			return nil
		}
	}
	return ErrNotFound
}

func (m *Manager) OwnsProxy(username, id string) bool {
	proxy, err := m.findProxy(id)
	return err == nil && proxy.Owner == username
}

func (m *Manager) RotateForUser(ctx context.Context, username, id string) (Proxy, error) {
	if !m.OwnsProxy(username, id) {
		return Proxy{}, ErrNotFound
	}
	return m.Rotate(ctx, id)
}

func (m *Manager) DeleteForUser(ctx context.Context, username, id string) error {
	if !m.OwnsProxy(username, id) {
		return ErrNotFound
	}
	return m.Delete(ctx, id)
}
