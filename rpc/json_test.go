package rpc

import (
	"encoding/json"
	"fmt"
	"testing"
)

type Account struct {
	Email    string
	password string
	Money    float64
}

func TestMarshal(t *testing.T) {
	account := Account{
		Email:    "caizhihong@qq.com",
		password: "12345678",
		Money:    100.5,
	}

	rs, err := json.Marshal(account)
	if err != nil {
		panic(err)
	}

	fmt.Println(rs)
	fmt.Println(string(rs))
}

func TestUnmarshal(t *testing.T) {
	jsonStrings := []string{`{}`, `[]`, `{"Email": "caizhihong@qq.com", "Money": 99.5}`}
	var jsonObj json.RawMessage
	for _, jsonStr := range jsonStrings {
		if err := json.Unmarshal([]byte(jsonStr), &jsonObj); err != nil {
			panic(err)
		}
		if len(jsonStr) > 4 {
			account := new(Account)
			if err := json.Unmarshal(jsonObj, account); err != nil {
				panic(err)
			}
			fmt.Println(account)
		}
		fmt.Println(string(jsonObj))
	}
}
