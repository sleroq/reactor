package main

import (
	"database/sql"
	"fmt"
	"github.com/go-faster/errors"
	"regexp"
	"strings"
)

func sessionFolder(phone string) string {
	var out []rune
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return "phone-" + string(out)
}

// Part function splits slice on specified position
func Part[T any](slice []T, length int) (new []T, modified []T) {
	if length > len(slice) {
		length = len(slice)
	}
	return slice[:length], slice[length:]
}

// reactionPositivity returns rating on scale from -10 to 10
// for any of Telegram reaction emojis
func reactionPositivity(emoticon string) int {
	switch emoticon {
	case "❤":
		return 9
	case "👍":
		return 8
	case "🤯":
		return 2
	case "🥰":
		return 9
	case "😢":
		return -6
	case "🍓":
		return 6
	case "🔥":
		return 9
	case "❤‍🔥":
		return 10
	case "😭":
		return -8
	case "🤔":
		return 0
	case "🆒":
		return 7
	case "😎":
		return 7
	case "💯":
		return 9
	case "🤝":
		return 8
	case "😨":
		return -7
	case "😱":
		return -8
	case "😡":
		return -9
	case "🤬":
		return -10
	case "😁":
		return 8
	case "👏":
		return 8
	case "👻":
		return 3
	case "👎":
		return -8
	case "🎉":
		return 9
	case "🤩":
		return 9
	case "🤮":
		return -10
	case "💩":
		return -5
	case "🙏":
		return 5
	case "👌":
		return 7
	case "🕊":
		return 6
	case "🤡":
		return -3
	case "🥱":
		return -4
	case "🥴":
		return -2
	case "🐳":
		return 5
	case "🌚":
		return -2
	case "🌭":
		return 4
	case "😆":
		return 9
	case "⚡️":
		return 3
	case "🍌":
		return 5
	case "🏆":
		return 9
	case "💔":
		return -10
	case "🖕":
		return -10
	case "🤨":
		return -1
	case "😐":
		return -1
	case "🍾":
		return 8
	case "💋":
		return 9
	case "😈":
		return -6
	case "😴":
		return -3
	case "🤓":
		return 6
	case "👨‍💻":
		return 6
	case "👀":
		return -1
	case "🎃":
		return -2
	case "💘":
		return 9
	case "🙈":
		return -2
	case "😇":
		return 8
	case "✍️":
		return -1
	case "🤗":
		return 9
	case "🫡":
		return -3
	case "🎅":
		return -2
	case "🎄":
		return -2
	case "☃️":
		return -2
	case "💅":
		return -1
	case "🤪":
		return -4
	case "🗿":
		return -1
	case "🙉":
		return -2
	case "😘":
		return 9
	case "🦄":
		return -2
	case "💊":
		return -4
	case "🙊":
		return -2
	case "👾":
		return -3
	case "🤷‍♂️":
		return -1
	case "🤷‍":
		return -1
	case "️🤷‍♀️":
		return -1
	default:
		fmt.Printf(`Warning: Unknown emoticon: "%s"`, emoticon)
		return 1
	}
}

var GOOD_WORDS = []string{
	"лол",
	"кек",
	"хех",
	"жиза",
	"жиз",
	"база",
	"секс",
	"нифега",
	"ржу",
	"ахаха",
	"кек",
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
	pattern := `(?i)(`
	excludePatten := `(?i)(`

	for i, word := range items {
		if words {
			pattern += fmt.Sprintf(`((^|\W)%s($|\W))`, word)
			excludePatten += fmt.Sprintf(`((^|\W)(not\s|not|no\s|no|не\s|не)%s($|\W))`, word)
		} else {
			pattern += fmt.Sprintf(`(%s)`, word)
			excludePatten += fmt.Sprintf(`((not\s|not|no\s|no|не\s|не)\w+%s)`, word)
		}

		if i+1 != len(items) {
			pattern += "|"
			excludePatten += "|"
		} else {
			pattern += ")"
			excludePatten += ")"
		}
	}

	result := false
	if res, err := regexp.MatchString(pattern, s); res {
		result = true
	} else if err != nil {
		return false, errors.Wrap(err, "matching body")
	}
	if res, err := regexp.MatchString(excludePatten, s); res {
		result = false
	} else if err != nil {
		return false, errors.Wrap(err, "matching body")
	}

	return result, nil
}

func positiveReplies(db *sql.DB, messageId, chatId int64) (map[int64]string, error) {
	messages, err := getReplies(db, chatId, messageId)
	if err != nil {
		return nil, errors.Wrap(err, "getting replies from database")
	}

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

		if res, err := regexp.MatchString(`(?i)^сука+`, body); res {
			positive = true
		} else if err != nil {
			return nil, errors.Wrap(err, "matching body")
		}
		if res, err := regexp.MatchString(`(?i)^\++$`, body); res {
			positive = true
		} else if err != nil {
			return nil, errors.Wrap(err, "matching body")
		}
		if res, err := regexp.MatchString(`(?i)^(плюс)+`, body); res {
			positive = true
		} else if err != nil {
			return nil, errors.Wrap(err, "matching body")
		}

		if positive {
			replies[reply.UserID] = body
		}
	}

	return replies, nil
}
