package helpers

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/go-faster/errors"
	"github.com/gotd/td/tdp"
	"github.com/gotd/td/tg"
	"github.com/sleroq/reactor/src/db"
)

var ratingTable = map[string]int{
	"❤":     9,
	"👍":     8,
	"🤯":     4,
	"🥰":     9,
	"😢":     -6,
	"🍓":     6,
	"🔥":     9,
	"❤‍🔥":   10,
	"😭":     2,
	"🤔":     2,
	"🆒":     7,
	"😎":     7,
	"💯":     9,
	"🤝":     8,
	"😨":     -3,
	"😱":     -3,
	"😡":     -9,
	"🤬":     -10,
	"😁":     8,
	"👏":     8,
	"👻":     5,
	"👎":     -8,
	"🎉":     9,
	"🤩":     9,
	"🤮":     -10,
	"💩":     -6,
	"🙏":     5,
	"👌":     7,
	"🕊":     6,
	"🤡":     -3,
	"🥱":     -6,
	"🥴":     -2,
	"🐳":     5,
	"🌚":     -2,
	"🌭":     4,
	"😆":     9,
	"🤣":     9,
	"⚡️":    6,
	"🍌":     5,
	"🏆":     9,
	"💔":     -8,
	"🖕":     -10,
	"🤨":     -1,
	"😐":     -3,
	"🍾":     8,
	"💋":     9,
	"😈":     2,
	"😴":     -3,
	"🤓":     3,
	"👨‍💻":   6,
	"👀":     1,
	"🎃":     -2,
	"💘":     9,
	"🙈":     -2,
	"😇":     7,
	"✍️":    2,
	"🤗":     9,
	"🫡":     -3,
	"🎅":     4,
	"🎄":     4,
	"☃️":    5,
	"☃":     5,
	"💅":     4,
	"🤪":     -4,
	"🗿":     -1,
	"🙉":     -2,
	"😘":     9,
	"🦄":     4,
	"💊":     -4,
	"🙊":     -2,
	"👾":     8,
	"🤷‍♂️":  1,
	"🤷‍":    1,
	"️🤷‍♀️": 1,
}

var positiveReplyPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^сукаa+`),
	regexp.MustCompile(`(?i)^я`),
	regexp.MustCompile(`(?i)^\++$`),
	regexp.MustCompile(`(?i)^(плюс)+`),
}

// ReactionPositivity returns rating on scale from -10 to 10
// for any of Telegram reaction emojis
func ReactionPositivity(emoticon string) (int, error) {
	if rating, ok := ratingTable[emoticon]; ok {
		return rating, nil
	}

	return 0, errors.Errorf("Unknown emoticon: %s", emoticon)
}

var GOOD_WORDS = []string{
	"лол",
	"мы",
	"кек",
	"хех",
	"жиза",
	"жиз",
	"умер",
	"база",
	"секс",
	"нифега",
	"ржу",
	"ахаха",
	"лмао",
	"хаха",
	"ого",
	"вау",
	"найс",
	"круто",
	"офигеть",
	"омг",
	"ппц",
	"жжешь",
	"браво",
	"респект",
	"класс",
	"супер",
	"молодец",
	"лучшая",
	"разрывная",
	"лучший",
	"угар",
	"гыгы",
	"красава",
	"мега",
	"ура",
	"збс",
	"агонь",
	"кайф",
	"топ",
	"лайк",
	"смех",
	"гениально",
	"ололо",
	"круть",
	"восторг",
	"шикарно",
	"бомба",
	"фантастика",
	"гуд",
	"кекусик",
}

var GOOD_PARTS = []string{
	"ор",
	"хах",
	"апх",
	"🥰",
	"👍",
	"❤",
	"согл",
	"база",
}

// matchMultiple function allows to match multiple letters/words in string
// using regexp (without iteration over string)
func matchMultiple(s string, items []string, words bool) (bool, error) {
	var pattern strings.Builder
	var excludePattern strings.Builder
	pattern.WriteString(`(?i)(`)
	excludePattern.WriteString(`(?i)(`)

	for i, word := range items {
		if words {
			fmt.Fprintf(&pattern, `((^|\W)%s($|\W))`, word)
			fmt.Fprintf(&excludePattern, `((^|\W)(not\s|not|no\s|no|не\s|не)%s($|\W))`, word)
		} else {
			fmt.Fprintf(&pattern, `(%s)`, word)
			fmt.Fprintf(&excludePattern, `((not\s|not|no\s|no|не\s|не)\w+%s)`, word)
		}

		if i+1 != len(items) {
			pattern.WriteString("|")
			excludePattern.WriteString("|")
		} else {
			pattern.WriteString(")")
			excludePattern.WriteString(")")
		}
	}

	result := false
	if res, err := regexp.MatchString(pattern.String(), s); res {
		result = true
	} else if err != nil {
		return false, errors.Wrap(err, "matching body")
	}
	if res, err := regexp.MatchString(excludePattern.String(), s); res {
		result = false
	} else if err != nil {
		return false, errors.Wrap(err, "matching body")
	}

	return result, nil
}

func PositiveReplies(messages []db.Message) (map[int64]string, error) {
	replies := make(map[int64]string)
	for _, reply := range messages {
		body := strings.TrimSpace(reply.Body)
		positive := false

		if len(body) < 10 {
			if match, err := matchMultiple(body, GOOD_PARTS, false); match {
				positive = true
			} else if err != nil {
				return nil, errors.Wrap(err, "matching good parts")
			}
		}
		if len(body) < 20 {
			if match, err := matchMultiple(body, GOOD_WORDS, true); match {
				positive = true
			} else if err != nil {
				return nil, errors.Wrap(err, "matching good words")
			}
		}

		for _, p := range positiveReplyPatterns {
			if p.MatchString(body) {
				positive = true
				break
			}
		}

		if positive {
			replies[reply.UserID] = body
		}
	}

	return replies, nil
}

func AsReactions(tgReactions []tg.MessagePeerReaction, chatID int64, messageID int) ([]db.Reaction, error) {
	var reactions []db.Reaction
	for _, tgReaction := range tgReactions {
		sentDate := time.Unix(int64(tgReaction.Date), 0)
		var userID int64
		switch p := tgReaction.PeerID.(type) {
		case *tg.PeerUser:
			userID = p.UserID
		default:
			return nil, fmt.Errorf(`unexpected peer type: "%s"`, p)
		}

		var emoticon string
		var documentId int64

		switch r := tgReaction.Reaction.(type) {
		case *tg.ReactionEmoji:
			emoticon = r.GetEmoticon()
		case *tg.ReactionCustomEmoji:
			documentId = r.GetDocumentID()
		default:
			return nil, fmt.Errorf("unexpected reaction type: %s", tgReaction.String())
		}

		reaction := db.Reaction{
			ChatID:     chatID,
			MessageID:  messageID,
			UserID:     userID,
			Emoticon:   emoticon,
			DocumentID: documentId,
			SentDate:   sentDate,
			Flags:      tgReaction.Flags,
			Big:        tgReaction.Big,
		}

		reactions = append(reactions, reaction)
	}

	return reactions, nil
}

func FormatObject(input any) string {
	o, ok := input.(tdp.Object)
	if !ok {
		// Handle tg.*Box values.
		rv := reflect.Indirect(reflect.ValueOf(input))
		for _, field := range rv.Fields() {
			if v, ok := field.Interface().(tdp.Object); ok {
				return FormatObject(v)
			}
		}

		return fmt.Sprintf("%T (not object)", input)
	}
	return tdp.Format(o)
}
