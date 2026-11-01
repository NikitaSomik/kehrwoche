-- Onboarding: a resident asks the bot to join, the admin approves or rejects,
-- the resident picks a room, then joins the group through a join-request link.
--
-- status is NULL for somebody the bot has only seen (the rows written before
-- this migration, and residents already in the group). 'rejected' doubles as
-- the bot's block list: /start from such a user is ignored.
--
-- room is kept only between choosing it and joining, when the bot copies it
-- into the member's Telegram tag; it also records the choice of residents
-- already in the group. The upper bound mirrors schedule.RoomCount.
ALTER TABLE users
    ADD COLUMN status VARCHAR(10),
    ADD COLUMN room   SMALLINT,
    ADD CONSTRAINT users_status_check CHECK (status IN ('pending', 'approved', 'rejected')),
    ADD CONSTRAINT users_room_check   CHECK (room BETWEEN 1 AND 8);
