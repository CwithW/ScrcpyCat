package controlplane

import "time"

// Store digests, never access tokens. Revocation survives control-plane restarts
// and expires with the token without affecting other logins for the same user.
func (s *MemoryStore) RevokeUserToken(digest string, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revokedUserTokens[digest] = expires
	return nil
}

func (s *MemoryStore) UserTokenRevoked(digest string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	expires, ok := s.revokedUserTokens[digest]
	return ok && time.Now().Before(expires)
}

func (s *PostgresStore) RevokeUserToken(digest string, expires time.Time) error {
	if err := s.inner.RevokeUserToken(digest, expires); err != nil {
		return err
	}
	return s.persist()
}

func (s *PostgresStore) UserTokenRevoked(digest string) bool {
	return s.inner.UserTokenRevoked(digest)
}
