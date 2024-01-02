package protocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUserKey_String(t *testing.T) {
	tests := []struct {
		name string
		k    UserKey
		want string
	}{
		{"test1", UserKey{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "AAAAAAAAAAAAAAAA"},
		{"test2", UserKey{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, "AAAQEAYEAUDAOCAJ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.k.String(); got != tt.want {
				t.Errorf("UserKey.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserKeyFromString(t *testing.T) {
	type args struct {
		keyStr string
	}
	tests := []struct {
		name    string
		args    args
		want    UserKey
		wantErr bool
	}{
		{"test1", args{"AAAAAAAAAAAAAAAA"}, UserKey{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, false},
		{"test2", args{"AAAQEAYEAUDAOCAJ"}, UserKey{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}, false},
		{"test3", args{"AAAQEAYEAUDAOCA"}, UserKey{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UserKeyFromString(tt.args.keyStr)
			if (err != nil) != tt.wantErr {
				t.Errorf("UserKeyFromString() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UserKeyFromString() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserKey_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		k       UserKey
		want    []byte
		wantErr bool
	}{
		{"test1", UserKey{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte(`"AAAAAAAAAAAAAAAA"`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.k.MarshalJSON()
			if (err != nil) != tt.wantErr {
				t.Errorf("UserKey.MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UserKey.MarshalJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserKey_UnmarshalJSON(t *testing.T) {
	type args struct {
		data []byte
	}
	tests := []struct {
		name    string
		k       *UserKey
		args    args
		wantErr bool
	}{
		{"test1", &UserKey{}, args{[]byte(`"AAAAAAAAAAAAAAAA"`)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.k.UnmarshalJSON(tt.args.data); (err != nil) != tt.wantErr {
				t.Errorf("UserKey.UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUserKeyToJSON(t *testing.T) {
	key := UserKey{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	jsonData, err := json.Marshal(key)
	if err != nil {
		t.Errorf("json.Marshal(key) error = %v", err)
	}
	if string(jsonData) != `"AAAQEAYEAUDAOCAJ"` {
		t.Errorf("json.Marshal(key) = %v, want %v", string(jsonData), `"AAAQEAYEAUDAOCAJ"`)
	}
}

func TestUserKeyFromJSON(t *testing.T) {
	var key UserKey
	jsonData := []byte(`"AAAQEAYEAUDAOCAJ"`)
	err := json.Unmarshal(jsonData, &key)
	if err != nil {
		t.Errorf("json.Unmarshal(jsonData, &key) error = %v", err)
	}
	if key != (UserKey{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}) {
		t.Errorf("json.Unmarshal(jsonData, &key) = %v, want %v", key, (UserKey{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}))
	}
}
