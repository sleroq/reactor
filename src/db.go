package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/go-faster/errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"time"
)

func saveMessage(msg *tg.Message, chatId int64, db *sql.DB) error {
	sentDate := time.Unix(int64(msg.Date), 0)

	var userId int64
	switch v := msg.FromID.(type) {
	case *tg.PeerUser:
		userId = v.UserID
	case nil:
		// Not saving this shit
		return nil
	default:
		return fmt.Errorf("unexpected message sender type: %s", v)
	}

	var fwdFromUser int64
	var fwdFromChannel int64

	switch v := msg.FwdFrom.FromID.(type) {
	case *tg.PeerUser:
		fwdFromUser = v.UserID
	case *tg.PeerChannel:
		fwdFromChannel = v.ChannelID
	case *tg.PeerChat:
		break
	case nil:
		break
	default:
		return fmt.Errorf("unexpected forward peer sender type %s", v)
	}

	hasPhoto := false
	switch msg.Media.(type) {
	case *tg.MessageMediaPhoto:
		hasPhoto = true
	}

	_, err := db.Exec(`
		insert into messages (
		    id,
		    sentDate,
		    chatId,
		    replyTo,
		    fwdFromUser,
		    fwdFromChannel,
		    withPhoto,
			userId,
		    body
		) values (
		    :id,
			:sentDate,
			:chatId,
			:replyTo,
		    :fwdFromUser,
		    :fwdFromChannel,
		    :withPhoto,
		    :userId,
		    :body
		)`,
		msg.ID,
		sentDate,
		chatId,
		msg.ReplyTo.ReplyToMsgID,
		fwdFromUser,
		fwdFromChannel,
		hasPhoto,
		userId,
		msg.Message)
	if err != nil {
		return errors.Wrap(err, "saving message to database")
	}

	return nil
}

func saveReaction(db *sql.DB, reaction Reaction) error {
	_, err := db.Exec(`
		insert into reactions (
			messageId,
			userId,
			positive,
			emoticon,
			documentId,
			sentDate,
			flags,
			big
		) values (
		    :messageId,
		    :userId,
		    :positive,
			:emoticon,
		    :documentId,
			:sentDate,
			:flags,
			:big
		)`,
		reaction.MessageId,
		reaction.UserId,
		reaction.Positive,
		reaction.Emoticon,
		reaction.DocumentID,
		reaction.SentDate,
		reaction.Flags,
		reaction.Big,
	)

	if err != nil {
		return errors.Wrap(err, "inserting new reaction")
	}

	return nil
}

func updateForwarded(db *sql.DB, chatId int64, messageId int64) error {
	_, err := db.Exec(`
		update messages
		set forwarded = 1
		where chatId = :chatId
			and id = :messageId
	`, chatId, messageId)

	return err
}

func deleteReaction(db *sql.DB, react Reaction) error {
	_, err := db.Exec(`
		delete from reactions
		where messageId = :messageId
			and userId = :userId
			and sentDate = :SentDate
	`, react.MessageId, react.UserId, react.SentDate)

	return err
}

func getChats(db *sql.DB) ([]Chat, error) {
	chatRows, err := db.Query(`select * from chats`)
	var chats []Chat
	for chatRows.Next() {
		var chat Chat
		var body string
		err = chatRows.Scan(
			&chat.Id,
			&chat.UpdatedAt,
			&chat.CreatedAt,
			&chat.AccessHash,
			&body,
		)
		if err != nil {
			return nil, errors.Wrap(err, "scanning result")
		}

		err = json.Unmarshal([]byte(body), &chat.Body)
		if err != nil {
			return nil, errors.Wrap(err, "unmarshalling result")
		}

		chats = append(chats, chat)
	}
	return chats, nil
}

func getMessagesAfter(db *sql.DB, chatID int64, date time.Time) ([]Message, error) {
	msgRows, err := db.Query(`
				select * from messages
				where SentDate > :startDate
				and chatId = :chatID
			`, date, chatID)
	if err != nil {
		return nil, errors.Wrap(err, "getting messages to check")
	}

	var messages []Message
	for msgRows.Next() {
		var message Message
		err = msgRows.Scan(
			&message.Id,
			&message.UpdatedAt,
			&message.SentDate,
			&message.ChatId,
			&message.Forwarded,
			&message.FwdFromUser,
			&message.FwdFromChannel,
			&message.withPhoto,
			&message.ReplyTo,
			&message.UserId,
			&message.Body,
		)
		if err != nil {
			return nil, errors.Wrap(err, "scanning result")
		}

		messages = append(messages, message)
	}

	return messages, nil
}

func getSavedReactions(db *sql.DB, chatId int64, messageId int64) ([]Reaction, error) {
	rows, err := db.Query(`
		select 
			r.messageId,
			r.userId,
			r.positive,
			r.emoticon,
			r.documentId,
			r.sentDate,
			r.flags,
			r.big
		from reactions r,
			messages m
		where r.messageId = m.id
			and m.id = :messageId
			and m.chatId = :chatId
	`, messageId, chatId)
	if err != nil {
		return nil, errors.Wrap(err, "querying reactions for message")
	}

	var reactions []Reaction
	for rows.Next() {
		var reaction Reaction
		err = rows.Scan(
			&reaction.MessageId,
			&reaction.UserId,
			&reaction.Positive,
			&reaction.Emoticon,
			&reaction.DocumentID,
			&reaction.SentDate,
			&reaction.Flags,
			&reaction.Big,
		)
		if err != nil {
			return nil, errors.Wrap(err, "scanning result")
		}

		reactions = append(reactions, reaction)
	}

	return reactions, nil
}

func saveChat(channel *tg.Channel, db *sql.DB) error {
	body, err := json.Marshal(channel)
	if err != nil {
		return errors.Wrap(err, "marshalling chat data")
	}

	_, err = db.Exec(`
		insert or ignore into chats (
			id,
			accessHash,
			body
		) values (
			:id,
			:accessHash,
			:body
		)
	`, channel.ID, channel.AccessHash, body)
	if err != nil {
		return errors.Wrap(err, "saving chat to database")
	}

	return nil
}

type Chat struct {
	Id         int64
	AccessHash int64
	UpdatedAt  time.Time
	CreatedAt  time.Time
	Body       map[string]any
}

type Message struct {
	Id             int64
	UpdatedAt      time.Time
	SentDate       time.Time
	ChatId         int64
	Forwarded      bool
	FwdFromUser    int64
	FwdFromChannel int64
	withPhoto      bool
	ReplyTo        int64
	UserId         int64
	Body           string
}

type Reaction struct {
	UserId     int64
	MessageId  int64
	Positive   bool
	Emoticon   string
	DocumentID int64
	SentDate   time.Time
	Flags      bin.Fields
	Big        bool
}

func setupDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "./memoq.db")
	if err != nil {
		return nil, errors.Wrap(err, "opening sqlite database")
	}

	_, err = db.Exec(`
		create table if not exists chats (
			id integer not null primary key,
			updatedAt datetime default (datetime('now')),
			createdAt datetime default (datetime('now')),
			accessHash integer not null,
			body text
		);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating chats table")
	}

	_, err = db.Exec(`
		create table if not exists messages (
			id integer not null primary key,
			updatedAt datetime default (datetime('now')),
			sentDate timestamp not null,
			chatId integer not null,
			forwarded integer default 0,
			fwdFromUser integer default 0,
			fwdFromChannel integer default 0,
			withPhoto integer not null,
			replyTo integer default 0,
			userId integer not null,
			body text not null,
			foreign key(chatId) references chats(id)
		);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating messages table")
	}

	_, err = db.Exec(`
		create table if not exists reactions (
			messageId integer not null,
			userId integer not null,
			positive integer not null,
			emoticon text,
			documentId integer,
			sentDate datetime not null,
			flags integer not null,
			big integer not null,
		    foreign key(messageId) references messages(id)
		);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating reactions table")
	}

	return db, nil
}
