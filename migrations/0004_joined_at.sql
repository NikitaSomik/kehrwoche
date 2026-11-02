-- joined_at: when the user last joined the WG group through the onboarding.
-- Never cleared, so a later request is known to come from a former member.
ALTER TABLE users ADD COLUMN joined_at TIMESTAMPTZ;
