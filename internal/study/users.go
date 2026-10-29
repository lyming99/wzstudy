package study

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"wzstudy/internal/auth"
)

// User is an authenticated personal-workspace principal. An administrator is
// also represented as a User, but its management capability is checked separately.
// Personal data uses this immutable ID, shared by all of the user's sessions.
type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Enabled     bool      `json:"enabled"`
	Revision    int64     `json:"revision"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type UserInput struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Password    string `json:"password,omitempty"`
}

type userRecord struct {
	Profile           User   `json:"profile"`
	PasswordHash      string `json:"password_hash"`
	CredentialVersion int64  `json:"credential_version"`
}

type userSessionRecord struct {
	ID                string    `json:"id"` // SHA-256 of the random token; never the token itself.
	UserID            string    `json:"user_id"`
	CredentialVersion int64     `json:"credential_version"`
	PrincipalKind     string    `json:"principal_kind,omitempty"` // empty is the legacy learning user; administrator is explicit.
	CreatedAt         time.Time `json:"created_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

const UserSessionLifetime = 8 * time.Hour
const administratorPrincipalKind = "system_administrator"

// The administrator's existing immutable ID is its private workspace owner.
// Never map a same-email learner's data to it; a same-ID collision fails closed.
func workspaceUserByID(tx Tx, id string) (User, error) {
	administrator, adminErr := readAdministrator(tx)
	if adminErr != nil && !errors.Is(adminErr, ErrNotFound) {
		return User{}, adminErr
	}
	if adminErr == nil && administrator.Profile.ID == id {
		_, err := recordAs[userRecord](tx, "learning_user", id)
		if err == nil {
			return User{}, ErrConflict
		}
		if !errors.Is(err, ErrNotFound) {
			return User{}, err
		}
		return administratorUser(administrator), nil
	}
	user, err := recordAs[userRecord](tx, "learning_user", id)
	if err != nil {
		return User{}, err
	}
	return user.Profile, nil
}

func normalizeUser(in UserInput) (UserInput, error) {
	var err error
	in.Email, err = auth.NormalizeEmail(in.Email)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if err != nil || in.DisplayName == "" || !validText(in.DisplayName, 120) || strings.ContainsAny(in.DisplayName, "\r\n") {
		return in, ErrInvalid
	}
	if in.Password != "" && auth.ValidatePassword(in.Password) != nil {
		return in, ErrInvalid
	}
	return in, nil
}

func saveUser(tx Tx, record userRecord) error {
	// The unified login reserves the administrator email for its independently
	// verified credentials. Never create another account with that email.
	administrator, err := readAdministrator(tx)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err == nil && administrator.Profile.Email == record.Profile.Email {
		// Existing legacy collisions may still be edited or disabled without
		// changing their email; only introducing a new collision is forbidden.
		previous, lookupErr := recordAs[userRecord](tx, "learning_user", record.Profile.ID)
		if lookupErr != nil && !errors.Is(lookupErr, ErrNotFound) {
			return lookupErr
		}
		if lookupErr != nil || previous.Profile.Email != record.Profile.Email {
			return ErrAccountEmailConflict
		}
	}
	all, err := recordsAs[userRecord](tx, "learning_user")
	if err != nil {
		return err
	}
	for _, other := range all {
		if other.Profile.ID != record.Profile.ID && other.Profile.Email == record.Profile.Email {
			return ErrAccountEmailConflict
		}
	}
	return recordSave(tx, "learning_user", record.Profile.ID, record)
}

func (s *Service) Users(ctx context.Context) (out []User, err error) {
	out = []User{}
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := recordsAs[userRecord](tx, "learning_user")
		if e != nil {
			return e
		}
		for _, v := range all {
			out = append(out, v.Profile)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
		return nil
	})
	return
}

func (s *Service) CreateUser(ctx context.Context, in UserInput) (out User, err error) {
	in, err = normalizeUser(in)
	if err != nil || in.Password == "" {
		return out, ErrInvalid
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return out, err
	}
	now := s.Now().UTC()
	v := userRecord{Profile: User{ID: uuid.NewString(), Email: in.Email, DisplayName: in.DisplayName, Enabled: in.Enabled == nil || *in.Enabled, Revision: 1, CreatedAt: now, UpdatedAt: now}, PasswordHash: hash, CredentialVersion: 1}
	err = s.Store.Update(ctx, func(tx Tx) error { return saveUser(tx, v) })
	if err == nil {
		out = v.Profile
	}
	return
}

// Password reset and disable/re-enable invalidate ALL previously issued sessions.
// Other profile updates do not change the personal-data owner ID.
func (s *Service) UpdateUser(ctx context.Context, id string, revision int64, in UserInput) (out User, err error) {
	in, err = normalizeUser(in)
	if err != nil {
		return
	}
	hash := ""
	if in.Password != "" {
		hash, err = auth.HashPassword(in.Password)
		if err != nil {
			return
		}
	}
	err = s.Store.Update(ctx, func(tx Tx) error {
		v, e := recordAs[userRecord](tx, "learning_user", id)
		if e != nil {
			return e
		}
		if v.Profile.Revision != revision {
			return ErrConflict
		}
		if hash != "" {
			v.PasswordHash = hash
			v.CredentialVersion++
		}
		if in.Enabled != nil && v.Profile.Enabled != *in.Enabled {
			v.CredentialVersion++
			v.Profile.Enabled = *in.Enabled
		}
		v.Profile.Email, v.Profile.DisplayName = in.Email, in.DisplayName
		v.Profile.Revision++
		v.Profile.UpdatedAt = s.Now().UTC()
		if e = saveUser(tx, v); e != nil {
			return e
		}
		out = v.Profile
		return nil
	})
	return
}

func saveAuthenticatedSession(tx Tx, userID string, version int64, kind, token string, now, expiry time.Time) error {
	all, err := recordsAs[userSessionRecord](tx, "learning_user_session")
	if err != nil {
		return err
	}
	own := []userSessionRecord{}
	for _, session := range all {
		if !session.ExpiresAt.After(now) {
			if err = tx.DeleteRecord("learning_user_session", session.ID); err != nil {
				return err
			}
		} else if session.UserID == userID {
			own = append(own, session)
		}
	}
	sort.Slice(own, func(i, j int) bool { return own[i].CreatedAt.Before(own[j].CreatedAt) })
	for len(own) >= 16 {
		if err = tx.DeleteRecord("learning_user_session", own[0].ID); err != nil {
			return err
		}
		own = own[1:]
	}
	v := userSessionRecord{ID: HashKey(token), UserID: userID, CredentialVersion: version, PrincipalKind: kind, CreatedAt: now, ExpiresAt: expiry}
	return recordSave(tx, "learning_user_session", v.ID, v)
}

func (s *Service) LoginUser(ctx context.Context, email, password string) (out User, token string, expires time.Time, err error) {
	email, normalizeErr := auth.NormalizeEmail(email)
	var record userRecord
	err = s.Store.View(ctx, func(tx Tx) error {
		all, e := recordsAs[userRecord](tx, "learning_user")
		if e != nil {
			return e
		}
		for _, v := range all {
			if normalizeErr == nil && v.Profile.Email == email {
				record = v
				break
			}
		}
		return nil
	})
	if err != nil {
		return
	}
	if record.Profile.ID == "" {
		auth.CheckDummyPassword(password)
		err = ErrUnauthorized
		return
	}
	if !auth.CheckPassword(record.PasswordHash, password) || !record.Profile.Enabled {
		err = ErrUnauthorized
		return
	}
	random, e := NewKey()
	if e != nil {
		err = e
		return
	}
	random = strings.Replace(random, "wzsk_", "wzus_", 1)
	now := s.Now().UTC()
	expiry := now.Add(UserSessionLifetime)
	err = s.Store.Update(ctx, func(tx Tx) error {
		current, e := recordAs[userRecord](tx, "learning_user", record.Profile.ID)
		if e != nil {
			return e
		}
		if !current.Profile.Enabled || current.CredentialVersion != record.CredentialVersion {
			return ErrUnauthorized
		}
		if e = saveAuthenticatedSession(tx, current.Profile.ID, current.CredentialVersion, "", random, now, expiry); e != nil {
			return e
		}
		out = current.Profile
		return nil
	})
	if err == nil {
		token, expires = random, expiry
	}
	return
}

// LoginAdministratorUser authenticates the existing administrator credential
// and issues the same kind of personal-workspace session used by learners. It
// does not merge any independently stored same-email learner account.
func (s *Service) LoginAdministratorUser(ctx context.Context, email, password string) (out User, token string, expires time.Time, err error) {
	var stored administratorRecord
	err = s.Store.View(ctx, func(tx Tx) error {
		var e error
		stored, e = readAdministrator(tx)
		return e
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return
	}
	normalized, normalizeErr := auth.NormalizeEmail(email)
	if err != nil || normalizeErr != nil || normalized != stored.Profile.Email {
		auth.CheckDummyPassword(password)
		err = ErrUnauthorized
		return
	}
	if !auth.CheckPassword(stored.PasswordHash, password) {
		err = ErrUnauthorized
		return
	}
	random, e := NewKey()
	if e != nil {
		err = e
		return
	}
	random = strings.Replace(random, "wzsk_", "wzus_", 1)
	now := s.Now().UTC()
	expiry := now.Add(UserSessionLifetime)
	err = s.Store.Update(ctx, func(tx Tx) error {
		current, e := readAdministrator(tx)
		if e != nil {
			return e
		}
		if current.Profile.ID != stored.Profile.ID || current.PasswordHash != stored.PasswordHash || administratorCredentialVersion(current) != administratorCredentialVersion(stored) {
			return ErrUnauthorized
		}
		user, e := workspaceUserByID(tx, current.Profile.ID)
		if e != nil {
			return e
		}
		if e = saveAuthenticatedSession(tx, user.ID, administratorCredentialVersion(current), administratorPrincipalKind, random, now, expiry); e != nil {
			return e
		}
		out = user
		return nil
	})
	if err == nil {
		token, expires = random, expiry
	}
	return
}

func userAccess(tx Tx, token string, now time.Time) (User, error) {
	if !strings.HasPrefix(token, "wzus_") || len(token) != 48 {
		return User{}, ErrUnauthorized
	}
	session, err := recordAs[userSessionRecord](tx, "learning_user_session", HashKey(token))
	if errors.Is(err, ErrNotFound) {
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, err
	}
	if !session.ExpiresAt.After(now) {
		return User{}, ErrUnauthorized
	}
	if session.PrincipalKind == administratorPrincipalKind {
		administrator, e := readAdministrator(tx)
		if errors.Is(e, ErrNotFound) {
			return User{}, ErrUnauthorized
		}
		if e != nil {
			return User{}, e
		}
		if administrator.Profile.ID != session.UserID || administratorCredentialVersion(administrator) != session.CredentialVersion {
			return User{}, ErrUnauthorized
		}
		return workspaceUserByID(tx, session.UserID)
	}
	if session.PrincipalKind != "" {
		return User{}, ErrUnauthorized
	}
	user, err := recordAs[userRecord](tx, "learning_user", session.UserID)
	if errors.Is(err, ErrNotFound) {
		return User{}, ErrUnauthorized
	}
	if err != nil {
		return User{}, err
	}
	if !user.Profile.Enabled || user.CredentialVersion != session.CredentialVersion {
		return User{}, ErrUnauthorized
	}
	return user.Profile, nil
}

func (s *Service) UserForSession(ctx context.Context, token string) (out User, err error) {
	err = s.Store.View(ctx, func(tx Tx) error { var e error; out, e = userAccess(tx, token, s.Now().UTC()); return e })
	return
}

// AdministratorForUserSession only accepts a session explicitly issued after
// verifying the administrator credential, never a same-email learner session.
func (s *Service) AdministratorForUserSession(ctx context.Context, token string) (out Administrator, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		user, e := userAccess(tx, token, s.Now().UTC())
		if e != nil {
			return e
		}
		session, e := recordAs[userSessionRecord](tx, "learning_user_session", HashKey(token))
		if e != nil {
			return e
		}
		if session.PrincipalKind != administratorPrincipalKind {
			return ErrUnauthorized
		}
		administrator, e := readAdministrator(tx)
		if e != nil {
			return e
		}
		if administrator.Profile.ID != user.ID {
			return ErrUnauthorized
		}
		out = administrator.Profile
		return nil
	})
	return
}

func (s *Service) LogoutUser(ctx context.Context, token string) error {
	if !strings.HasPrefix(token, "wzus_") {
		return ErrUnauthorized
	}
	return s.Store.Update(ctx, func(tx Tx) error { return tx.DeleteRecord("learning_user_session", HashKey(token)) })
}

func (s *Service) ChangeUserPassword(ctx context.Context, token, current, password string) error {
	if auth.ValidatePassword(password) != nil {
		return ErrInvalid
	}
	user, err := s.UserForSession(ctx, token)
	if err != nil {
		return err
	}
	if _, adminErr := s.AdministratorForUserSession(ctx, token); adminErr == nil {
		return s.changeAdministratorUserPassword(ctx, token, current, password)
	} else if !errors.Is(adminErr, ErrUnauthorized) {
		return adminErr
	}
	var record userRecord
	err = s.Store.View(ctx, func(tx Tx) error {
		var e error
		record, e = recordAs[userRecord](tx, "learning_user", user.ID)
		return e
	})
	if err != nil {
		return err
	}
	if !auth.CheckPassword(record.PasswordHash, current) {
		return ErrUnauthorized
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.Store.Update(ctx, func(tx Tx) error {
		if _, e := userAccess(tx, token, s.Now().UTC()); e != nil {
			return e
		}
		v, e := recordAs[userRecord](tx, "learning_user", user.ID)
		if e != nil {
			return e
		}
		if v.CredentialVersion != record.CredentialVersion {
			return ErrConflict
		}
		v.PasswordHash = hash
		v.CredentialVersion++
		v.Profile.Revision++
		v.Profile.UpdatedAt = s.Now().UTC()
		return saveUser(tx, v)
	})
}

func (s *Service) changeAdministratorUserPassword(ctx context.Context, token, current, password string) error {
	var stored administratorRecord
	if err := s.Store.View(ctx, func(tx Tx) error {
		var e error
		stored, e = readAdministrator(tx)
		return e
	}); err != nil {
		return err
	}
	if !auth.CheckPassword(stored.PasswordHash, current) {
		return ErrUnauthorized
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.Store.Update(ctx, func(tx Tx) error {
		if _, e := userAccess(tx, token, s.Now().UTC()); e != nil {
			return e
		}
		session, e := recordAs[userSessionRecord](tx, "learning_user_session", HashKey(token))
		if e != nil {
			return e
		}
		admin, e := readAdministrator(tx)
		if e != nil {
			return e
		}
		if session.PrincipalKind != administratorPrincipalKind || session.UserID != admin.Profile.ID ||
			admin.Profile.ID != stored.Profile.ID || administratorCredentialVersion(admin) != administratorCredentialVersion(stored) || admin.PasswordHash != stored.PasswordHash {
			return ErrConflict
		}
		admin.PasswordHash = hash
		admin.CredentialVersion = administratorCredentialVersion(admin) + 1
		admin.UpdatedAt = s.Now().UTC()
		return recordSave(tx, administratorKind, administratorID, admin)
	})
}

// The historical learning schema calls its owner column client_id. Adapt a user
// session to that immutable owner ID, WITHOUT creating a Client or sharing keys.
// Legacy client-key access is retained inside the service for backwards
// compatible administrative tooling; HTTP personal endpoints require users.
func userLearningPrincipal(tx Tx, token string) (Client, error) {
	v, err := userAccess(tx, token, time.Now().UTC())
	if err != nil {
		return Client{}, err
	}
	if scoped, ok := tx.(*workspaceTx); ok && scoped.owner != v.ID {
		return Client{}, ErrUnauthorized
	}
	return Client{OwnerID: v.ID, ID: v.ID, Slug: v.ID, Name: v.DisplayName, Enabled: true, Permissions: []string{PermissionRead, PermissionSync, PermissionFeedback}}, nil
}

func personalOwnerExists(tx Tx, id string) error {
	_, err := recordAs[clientRecord](tx, "client", id)
	return err
}

func (s *Service) User(ctx context.Context, id string) (out User, err error) {
	err = s.Store.View(ctx, func(tx Tx) error {
		var e error
		out, e = workspaceUserByID(tx, id)
		return e
	})
	return
}
