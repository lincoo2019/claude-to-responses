package jsonx

import jsoniter "github.com/json-iterator/go"

var api = jsoniter.ConfigCompatibleWithStandardLibrary

func Marshal(v any) ([]byte, error) {
	return api.Marshal(v)
}

func Unmarshal(data []byte, v any) error {
	return api.Unmarshal(data, v)
}

func FirstNonSpaceByte(data []byte) byte {
	for _, b := range data {
		switch b {
		case ' ', '\n', '\r', '\t':
			continue
		default:
			return b
		}
	}
	return 0
}
