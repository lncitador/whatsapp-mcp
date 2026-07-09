package wa

import "testing"

func TestStatusTransitions(t *testing.T) {
	c := &Client{}
	c.setState(AuthConnecting, "", "")
	if s := c.Status(); s.State != AuthConnecting {
		t.Fatalf("state = %s", s.State)
	}
	c.setState(AuthWaitingQR, "QRDATA", "scan me")
	s := c.Status()
	if s.State != AuthWaitingQR || s.QRCode != "QRDATA" || s.Message != "scan me" {
		t.Fatalf("got %+v", s)
	}
	c.setState(AuthConnected, "", "")
	if s := c.Status(); s.QRCode != "" {
		t.Fatalf("QR must be cleared on connect: %+v", s)
	}
}
