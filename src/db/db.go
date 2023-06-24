package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/go-faster/errors"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/exp/slices"
	"strconv"
	"strings"
	"time"
)

func SaveMessage(msg *tg.Message, chatID int64, db *sql.DB) error {
	sentDate := time.Unix(int64(msg.Date), 0)

	var userId int64
	switch v := msg.FromID.(type) {
	case *tg.PeerUser:
		userId = v.UserID
	case *tg.PeerChannel:
	case nil:
		// Not saving this shit (sorry, too lazy)
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
		    body,
		    groupedId
		) values (
		    :ID,
			:sentDate,
			:chatID,
			:replyTo,
		    :fwdFromUser,
		    :fwdFromChannel,
		    :withPhoto,
		    :userID,
		    :body,
		    :groupedID
		)`,
		msg.ID,
		sentDate,
		chatID,
		msg.ReplyTo.ReplyToMsgID,
		fwdFromUser,
		fwdFromChannel,
		hasPhoto,
		userId,
		msg.Message,
		msg.GroupedID)
	if err != nil {
		return errors.Wrap(err, "saving message to database")
	}

	return nil
}

func UpdateMessageBody(db *sql.DB, msg Message) error {
	_, err := db.Exec(`
		update messages
		set body = :body
		where id = :id
		and chatId = :chatID
	`, msg.Body, msg.ID, msg.ChatID)

	if err != nil {
		return errors.Wrap(err, "updating message body")
	}

	return nil
}

func SaveReaction(db *sql.DB, reaction Reaction) error {
	_, err := db.Exec(`
		insert into reactions (
            chatId,
			messageId,
			userId,
			emoticon,
			documentId,
			sentDate,
			flags,
			big
		) values (
		    :chatID,
		    :messageID,
		    :userID,
			:emoticon,
		    :documentID,
			:sentDate,
			:flags,
			:big
		)`,
		reaction.ChatID,
		reaction.MessageID,
		reaction.UserID,
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

func UpdateForwarded(db *sql.DB, chatID int64, messageID int) error {
	_, err := db.Exec(`
		update messages
		set forwarded = 1
		where chatId = :chatID
			and id = :messageID
	`, chatID, messageID)

	return err
}

func DeleteReaction(db *sql.DB, react Reaction) error {
	_, err := db.Exec(`
		delete from reactions
		where messageId = :messageID
		    and chatId = :chatID
			and userId = :userID
			and sentDate = :SentDate
	`, react.MessageID, react.ChatID, react.UserID, react.SentDate)

	return err
}

func ScanMessageRows(rows *sql.Rows) ([]Message, error) {
	var messages []Message
	for rows.Next() {
		var message Message
		err := rows.Scan(
			&message.ID,
			&message.UpdatedAt,
			&message.SentDate,
			&message.ChatID,
			&message.Forwarded,
			&message.FwdFromUser,
			&message.FwdFromChannel,
			&message.WithPhoto,
			&message.ReplyTo,
			&message.UserID,
			&message.Body,
			&message.GroupedID,
		)
		if err != nil {
			return nil, errors.Wrap(err, "scanning message row")
		}

		messages = append(messages, message)
	}

	return messages, nil
}

func GetMessage(db *sql.DB, chatID int64, msgID int) (Message, error) {
	msgRows, err := db.Query(`
		select *
		from messages
		where id = :messageID
			and chatId = :chatID
	`, msgID, chatID)
	if err != nil {
		return Message{}, errors.Wrap(err, "getting message replies")
	}

	messages, err := ScanMessageRows(msgRows)
	if err != nil {
		return Message{}, errors.Wrap(err, "scanning message rows")
	}

	if len(messages) > 0 {
		return messages[0], nil
	}

	return Message{}, fmt.Errorf("not found")
}

func GetReplies(db *sql.DB, chatID int64, replyTo int) ([]Message, error) {
	msgRows, err := db.Query(`
		select *
		from messages
		where replyTo = :messageID
			and chatId = :chatID
	`, replyTo, chatID)
	if err != nil {
		return nil, errors.Wrap(err, "getting message replies")
	}

	return ScanMessageRows(msgRows)
}

func GetMessagesAfter(db *sql.DB, chatID int64, date time.Time) ([]Message, error) {
	msgRows, err := db.Query(`
				select * from messages
				where SentDate > :startDate
				and chatId = :chatID
			`, date, chatID)
	if err != nil {
		return nil, errors.Wrap(err, "getting messages to check")
	}

	return ScanMessageRows(msgRows)
}

func GetMessagesGroup(db *sql.DB, groupedID int64) ([]Message, error) {
	msgRows, err := db.Query(`
				select * from messages
				where groupedId = :groupedID
			`, groupedID)
	if err != nil {
		return nil, errors.Wrap(err, "getting messages to check")
	}

	return ScanMessageRows(msgRows)
}

func GetSavedReactions(db *sql.DB, chatID int64, messageID int) ([]Reaction, error) {
	rows, err := db.Query(`
		select 
		    chatId,
			messageId,
			userId,
			emoticon,
			documentId,
			sentDate,
			flags,
			big
		from reactions
		where chatId = :chatID
		    and messageId = :messageID
	`, chatID, messageID)
	if err != nil {
		return nil, errors.Wrap(err, "querying reactions for message")
	}

	var reactions []Reaction
	for rows.Next() {
		var reaction Reaction
		err = rows.Scan(
			&reaction.ChatID,
			&reaction.MessageID,
			&reaction.UserID,
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

func SaveChat(channel *tg.Channel, db *sql.DB) error {
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
			:ID,
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
	ID         int64
	AccessHash int64
	UpdatedAt  time.Time
	CreatedAt  time.Time
	Body       map[string]any
}

type Message struct {
	ID             int
	UpdatedAt      time.Time
	SentDate       time.Time
	ChatID         int64
	Forwarded      bool
	FwdFromUser    int64
	FwdFromChannel int64
	WithPhoto      bool
	ReplyTo        int64
	UserID         int64
	Body           string
	GroupedID      int64
}

type Reaction struct {
	ChatID     int64
	MessageID  int
	UserID     int64
	Emoticon   string
	DocumentID int64
	SentDate   time.Time
	Flags      bin.Fields
	Big        bool
}

func SetupDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "./reactor.db")
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
			id integer not null,
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
			groupedId integer default 0,
			primary key (id, chatId),
			foreign key(chatId) references chats(id)
		);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating messages table")
	}

	_, err = db.Exec(`
		create table if not exists reactions (
			messageId integer not null,
			chatId integer not null,
			userId integer not null,
			emoticon text,
			documentId integer,
			sentDate datetime not null,
			flags integer not null,
			big integer not null,
			foreign key(messageId, chatId) references messages(id, chatId)
		);
	`)
	if err != nil {
		return nil, errors.Wrap(err, "creating reactions table")
	}

	return db, nil
}

func SyncPeerReactions(botdb *sql.DB, old, new []Reaction) (err error) {
	for _, react := range new {
		hasNewReaction := slices.ContainsFunc(old, func(el Reaction) bool {
			if el.SentDate.Equal(react.SentDate) &&
				el.UserID == react.UserID &&
				el.ChatID == react.ChatID {
				return true
			}
			return false
		})

		if !hasNewReaction {
			err = SaveReaction(botdb, react)
			if err != nil {
				return errors.Wrap(err, "saving new reaction")
			}
		}
	}

	for _, oldReact := range old {
		hasOldReaction := slices.ContainsFunc(new, func(el Reaction) bool {
			if el.SentDate.Equal(oldReact.SentDate) &&
				el.UserID == oldReact.UserID &&
				el.ChatID == oldReact.ChatID {
				return true
			}
			return false
		})
		if !hasOldReaction {
			err = DeleteReaction(botdb, oldReact)
			if err != nil {
				return errors.Wrap(err, "deleting reaction")
			}
		}
	}

	return nil
}

func GetOnlySavedChats(sources []tg.InputPeerChannel, db *sql.DB) ([]Chat, error) {
	var IDs []string

	for _, source := range sources {
		IDs = append(IDs, strconv.FormatInt(source.ChannelID, 10))
	}

	if len(IDs) == 0 {
		return []Chat{}, nil
	}

	query := fmt.Sprintf(`
		select * from chats
		where id in (%s)
	`, strings.Join(IDs, ", "))

	chatRows, err := db.Query(query)
	var chats []Chat
	for chatRows.Next() {
		var chat Chat
		var body string
		err = chatRows.Scan(
			&chat.ID,
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
