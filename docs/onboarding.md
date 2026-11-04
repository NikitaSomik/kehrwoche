# Onboarding flow

What happens after somebody sends `/start` to the bot in private. The steps
are described in the README's [Onboarding](../README.md#onboarding) section;
this is the same flow with every branch.

```mermaid
flowchart TD
    start(["/start in private"]) --> role{"Role in the WG group<br/>(getChatMember)"}
    role -- "creator / administrator" --> admin["Command menu + full command list"]
    role -- "member" --> member["Greeting: already in the group"]
    role -- "kicked" --> silent1(["Silence"])
    role -- "left / never joined" --> row{"users.status"}

    row -- "rejected" --> silent2(["Silence: block list"])
    row -- "pending" --> waiting["Request is still waiting"]
    row -- "approved, no room" --> rooms
    row -- "approved, room set" --> link
    row -- "none" --> request["status = pending"]

    request --> card["Card to ADMIN_CHAT_ID:<br/>name, @username, id,<br/>👤 profile, ✅ / ❌"]
    card --> press{"Pressed by an admin<br/>of the WG group?"}
    press -- "no" --> refused["Das geht leider nicht."]
    press -- "❌" --> rejected["status = rejected<br/>user is told once"]
    press -- "✅" --> approved["status = approved"]
    approved --> rooms["Room buttons 1–8"]
    rooms -- "pressed in the user's own chat" --> room["users.room = n"]
    room --> link{"Stored link valid<br/>for more than 1 h?"}
    link -- "yes" --> resend["Send the same link"]
    link -- "no" --> newlink["New join-request link, 24 h<br/>old link revoked"]
    resend --> join
    newlink --> join(["User opens the link:<br/>chat_join_request"])
    resend -. "Zimmer ändern" .-> rooms
    newlink -. "Zimmer ändern" .-> rooms

    join --> check{"From this user, status approved,<br/>room set, through their exact link?"}
    check -- "no" --> manual(["Left to the group admins"])
    check -- "yes" --> done["Approve request<br/>status cleared, link revoked<br/>member tag = room number<br/>welcome, or „wieder da“<br/>for a former member"]
```

Dashed arrows are the „Zimmer ändern“ button under the link: the room buttons
again, for an approved user who hasn't joined yet.
