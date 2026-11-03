-- left_at: when the user last left the WG group or was removed from it, as
-- the chat_member update reported it. Cleared when they join again. With
-- joined_at it marks a former member, including those who joined before the
-- bot did.
ALTER TABLE users ADD COLUMN left_at TIMESTAMPTZ;
