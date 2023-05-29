package main

import "fmt"

func sessionFolder(phone string) string {
	var out []rune
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			out = append(out, r)
		}
	}
	return "phone-" + string(out)
}

func Part[T any](slice []T, length int) (new []T, modified []T) {
	if length > len(slice) {
		length = len(slice)
	}
	return slice[:length], slice[length:]
}

func reactionPositivity(emoticon string) int {
	switch emoticon {
	case "❤️":
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
