-- +goose Up

-- The built-in administrator account.
--
-- Silt used to be open unless SILT_PASSWORD_HASH was set, which meant the
-- default install had no authentication at all and the safe configuration was
-- the one you had to know to ask for. This inverts that: the account exists
-- from first boot, and the first person to reach the UI is asked to choose a
-- password before anything else is reachable.
--
-- One row, enforced by the CHECK. There is one Silt and one administrator; a
-- table of users would be a user system nobody asked for, and the way to have
-- more than one identity is to point Silt at a provider that already manages
-- them.
CREATE TABLE local_account (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  -- bcrypt. Empty means the account has not been claimed yet.
  password_hash TEXT NOT NULL DEFAULT '',
  -- 0 disables password sign-in without deleting the account, so a linked
  -- provider identity still reaches it.
  enabled       INTEGER NOT NULL DEFAULT 1,
  -- The provider subject this account is linked to, if any. An OpenID Connect
  -- login carrying it is this account rather than a separate identity.
  oidc_subject  TEXT NOT NULL DEFAULT '',
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
);

-- +goose Down
DROP TABLE local_account;
