-- Onboarding state. status: NULL (no open request), pending, approved,
-- rejected (ignored from then on). room: the chosen room; the upper bound
-- mirrors schedule.RoomCount. invite_link: the join-request link issued to
-- this user, the only link their request is approved through, valid until
-- invite_expires_at.
ALTER TABLE users
    ADD COLUMN status            VARCHAR(10),
    ADD COLUMN room              SMALLINT,
    ADD COLUMN invite_link       TEXT,
    ADD COLUMN invite_expires_at TIMESTAMPTZ,
    ADD CONSTRAINT users_status_check CHECK (status IN ('pending', 'approved', 'rejected')),
    ADD CONSTRAINT users_room_check   CHECK (room BETWEEN 1 AND 8);
