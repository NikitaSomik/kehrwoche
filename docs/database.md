# Database

Two tables, applied by `cmd/migrate` from `migrations/*.sql`. There is no
foreign key between them: a schedule row names a room (`Zimmer 3`), a user row
holds a room number (`3`), and `schedule.RoomNo` / `schedule.ParseRoomNo`
convert between the two.

```mermaid
erDiagram
  schedules {
    bigint id PK
    varchar duty_type "toilet1, toilet2, laundry, hall, floor"
    date duty_date "the duty day: Fri, or Tue/Fri for laundry"
    varchar room "Zimmer 1 .. Zimmer 8"
  }
  users {
    bigint id PK
    bigint tg_user_id UK "Telegram user id"
    timestamptz created_at "first sighting"
    timestamptz updated_at "last sighting"
    varchar status "NULL, pending, approved, rejected"
    smallint room "1 .. 8"
    text invite_link "the one link their request is approved through"
    timestamptz invite_expires_at
    timestamptz joined_at "set on joining, never cleared"
    timestamptz left_at "set on leaving, cleared on joining"
  }
```

- `schedules` is unique on `(duty_type, duty_date)`: one room per duty and day.
- `users` is unique on `tg_user_id`. No names are stored.
- `users.status` is the onboarding's state; `rejected` is the block list.
- `users.room` is limited to 1..8 by `users_room_check`, which mirrors
  `schedule.RoomCount` by hand.
- `joined_at` or `left_at` set marks a former member.
