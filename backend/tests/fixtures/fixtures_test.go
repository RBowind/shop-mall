package fixtures

import (
	"reflect"
	"testing"
)

func TestFixturesAreDeterministicAndCoverRequiredEntities(t *testing.T) {
	if len(Users) == 0 || len(Products) == 0 || len(Addresses) == 0 || len(Orders) == 0 || len(Administrators) == 0 {
		t.Fatal("fixtures must contain users, products, addresses, orders, and administrators")
	}

	first := Snapshot()
	second := Snapshot()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("fixture snapshots differ: first=%#v second=%#v", first, second)
	}
	if stringsContainSessionKey(first) {
		t.Fatal("fixture snapshot must not contain session_key data")
	}
}

func stringsContainSessionKey(value any) bool {
	switch value := value.(type) {
	case string:
		return value == "session_key" || value == "fake-session-key" || value == "wx-session-key"
	case []User:
		for _, item := range value {
			if stringsContainSessionKey(item.OpenID) || stringsContainSessionKey(item.Nickname) {
				return true
			}
		}
	case []Product:
		for _, item := range value {
			if stringsContainSessionKey(item.Name) || stringsContainSessionKey(item.Description) {
				return true
			}
		}
	case []Address:
		for _, item := range value {
			if stringsContainSessionKey(item.Receiver) || stringsContainSessionKey(item.Detail) {
				return true
			}
		}
	case []Order:
		for _, item := range value {
			if stringsContainSessionKey(item.OrderNo) || stringsContainSessionKey(item.Address) {
				return true
			}
		}
	case []Administrator:
		for _, item := range value {
			if stringsContainSessionKey(item.Username) || stringsContainSessionKey(item.PasswordHash) {
				return true
			}
		}
	case FixtureSnapshot:
		return stringsContainSessionKey(value.Users) || stringsContainSessionKey(value.Products) || stringsContainSessionKey(value.Addresses) || stringsContainSessionKey(value.Orders) || stringsContainSessionKey(value.Administrators)
	}
	return false
}
