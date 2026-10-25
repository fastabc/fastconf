package main

import "testing"

func TestMissedNotificationRecovery(t *testing.T) {
	if err := example(); err != nil {
		t.Fatal(err)
	}
}
