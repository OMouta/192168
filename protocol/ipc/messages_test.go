package ipc

import (
	"encoding/json"
	"testing"
)

func TestRequestUnmarshalParams(t *testing.T) {
	raw := `{"id":"req_1","method":"ConnectGroup","params":{"groupId":"grp_9"}}`

	var req Request
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.Method != MethodConnect {
		t.Errorf("method = %q, want %q", req.Method, MethodConnect)
	}

	var params GroupParams
	if err := req.UnmarshalParams(&params); err != nil {
		t.Fatalf("UnmarshalParams: %v", err)
	}
	if params.GroupID != "grp_9" {
		t.Errorf("groupId = %q, want %q", params.GroupID, "grp_9")
	}
}

func TestUnmarshalParamsOnMethodWithNoParams(t *testing.T) {
	req := Request{ID: "req_2", Method: MethodGetState}

	var params GroupParams
	if err := req.UnmarshalParams(&params); err != nil {
		t.Fatalf("UnmarshalParams: %v", err)
	}
	if params.GroupID != "" {
		t.Errorf("groupId = %q, want empty", params.GroupID)
	}
}

func TestEventUnmarshalData(t *testing.T) {
	data, err := json.Marshal(PeerStateChangedData{DeviceID: "dev_3", State: PeerDirect})
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}

	var got PeerStateChangedData
	if err := (Event{Event: EventPeerStateChanged, Data: data}).UnmarshalData(&got); err != nil {
		t.Fatalf("UnmarshalData: %v", err)
	}
	if got.DeviceID != "dev_3" || got.State != PeerDirect {
		t.Errorf("data = %+v", got)
	}
}

// The client is a separate language, so the JSON field names are the contract.
// This pins the ones a hand written C# model has to match.
func TestStateFieldNames(t *testing.T) {
	latency := 18
	encoded, err := json.Marshal(State{
		Connection: StateConnected,
		ServerURL:  "https://api.192168.lol",
		GroupID:    "grp_9",
		VirtualIP:  "10.69.0.1",
		Peers: []PeerView{{
			DeviceID:  "dev_3",
			Nickname:  "João",
			VirtualIP: "10.69.0.2",
			State:     PeerDirect,
			LatencyMS: &latency,
		}},
	})
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}

	var generic map[string]any
	if err := json.Unmarshal(encoded, &generic); err != nil {
		t.Fatalf("unmarshal state: %v", err)
	}
	for _, key := range []string{"connection", "serverUrl", "serverOnline", "groupId", "virtualIp", "peers"} {
		if _, ok := generic[key]; !ok {
			t.Errorf("state is missing field %q", key)
		}
	}

	peer := generic["peers"].([]any)[0].(map[string]any)
	for _, key := range []string{"deviceId", "nickname", "virtualIp", "state", "latencyMs"} {
		if _, ok := peer[key]; !ok {
			t.Errorf("peer is missing field %q", key)
		}
	}
}
