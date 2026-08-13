package db

import (
	"database/sql"
	"testing"

	"github.com/gotd/td/tg"
)

func TestSaveMessageWithStory(t *testing.T) {
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	_, err = database.Exec(`
		create table messages (
			id integer,
			updatedAt datetime default (datetime('now')),
			sentDate timestamp,
			chatId integer,
			forwarded integer default 0,
			fwdFromUser integer default 0,
			fwdFromChannel integer default 0,
			withPhoto integer,
			replyTo integer default 0,
			userId integer,
			body text,
			groupedId integer default 0,
			primary key (id, chatId)
		)
	`)
	if err != nil {
		t.Fatal(err)
	}

	message, err := SaveMessage(&tg.Message{
		ID:      1,
		FromID:  &tg.PeerUser{UserID: 2},
		Media:   &tg.MessageMediaStory{Peer: &tg.PeerUser{UserID: 3}, ID: 4},
		ReplyTo: &tg.MessageReplyStoryHeader{Peer: &tg.PeerUser{UserID: 3}, StoryID: 4},
	}, 5, database)
	if err != nil {
		t.Fatal(err)
	}
	if !message.WithPhoto {
		t.Error("story message should be categorized as a photo")
	}
	if message.ReplyTo != 0 {
		t.Errorf("story reply ID must not be stored as a message reply: got %d", message.ReplyTo)
	}

	stored, err := GetMessage(database, 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.WithPhoto {
		t.Error("stored story message should be categorized as a photo")
	}
}
