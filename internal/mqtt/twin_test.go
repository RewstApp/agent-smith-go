package mqtt

import "testing"

func TestParseTwinResponse(t *testing.T) {
	cases := []struct {
		topic  string
		status int
		rid    string
		ok     bool
	}{
		{"$iothub/twin/res/200/?$rid=1001", 200, "1001", true},
		{"$iothub/twin/res/204/?$rid=1&$version=7", 204, "1", true},
		{"$iothub/twin/res/429/?$rid=1", 429, "1", true},
		{"$iothub/twin/res/200/", 200, "", true},
		{"$iothub/twin/res/abc/?$rid=1", 0, "", false},
		{"$iothub/twin/PATCH/properties/desired/?$version=3", 0, "", false},
		{"devices/d/messages/devicebound/", 0, "", false},
	}
	for _, c := range cases {
		status, rid, ok := ParseTwinResponse(c.topic)
		if status != c.status || rid != c.rid || ok != c.ok {
			t.Errorf("%s: got (%d, %q, %t), want (%d, %q, %t)",
				c.topic, status, rid, ok, c.status, c.rid, c.ok)
		}
	}
}

func TestIsDesiredPatch(t *testing.T) {
	if !IsDesiredPatch("$iothub/twin/PATCH/properties/desired/?$version=3") {
		t.Error("a desired PATCH topic was not recognised")
	}
	for _, topic := range []string{
		"$iothub/twin/PATCH/properties/reported/?$rid=1",
		"$iothub/twin/res/200/?$rid=1",
		"devices/d/messages/devicebound/",
	} {
		if IsDesiredPatch(topic) {
			t.Errorf("%s read as a desired PATCH", topic)
		}
	}
}
