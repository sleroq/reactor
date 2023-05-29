package main

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

func removeDuplicate[T string | int | int64](sliceList []T) []T {
	allKeys := make(map[T]bool)
	var list []T
	for _, item := range sliceList {
		if _, value := allKeys[item]; !value {
			allKeys[item] = true
			list = append(list, item)
		}
	}
	return list
}
