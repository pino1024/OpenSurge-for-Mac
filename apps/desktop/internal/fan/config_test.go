package fan

import (
	"os"
	"reflect"
	"testing"
)

func TestSaveAndLoadFanConfig(t *testing.T) {
	path := t.TempDir() + "/fan.json"
	cfg := Config{Visible: true, Enabled: false, Points: []Point{{C: 55, P: 0}, {C: 70, P: 30}, {C: 85, P: 100}}}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("loaded %#v", got)
	}
	missing, err := Load(t.TempDir() + "/missing.json")
	if err != nil || !reflect.DeepEqual(missing, Default()) {
		t.Fatalf("missing file: %#v %v", missing, err)
	}
	if err := Save(path, Config{Visible: true, Enabled: true, Points: []Point{{C: 90, P: 40}, {C: 91, P: 20}}}); err == nil {
		t.Fatal("curve points must rise in temperature and speed")
	}
}

func TestLegacyDisplaySwitchStaysVisibleAndKeepsTheCurve(t *testing.T) {
	path := t.TempDir() + "/fan.json"
	if err := os.WriteFile(path, []byte("{\"enabled\":false,\"start\":55,\"full\":80,\"hysteresis\":4}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Visible: false, Enabled: true, Points: []Point{{C: 55, P: 0}, {C: 67, P: 26}, {C: 80, P: 100}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrated %#v", got)
	}
	again, err := Load(path)
	if err != nil || !reflect.DeepEqual(again, want) {
		t.Fatalf("rewritten %#v %v", again, err)
	}
}

func TestReadFanStatus(t *testing.T) {
	path := t.TempDir() + "/fan-status.json"
	if err := os.WriteFile(path, []byte("{\"temp\":61.2,\"rpm\":1200,\"percent\":5.1,\"active\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadStatus(path)
	if !ok || got.Temp != 61.2 || got.RPM != 1200 || got.Percent != 5.1 || !got.Active {
		t.Fatalf("status %#v %v", got, ok)
	}
	if _, ok := ReadStatus(path + ".missing"); ok {
		t.Fatal("missing status looked present")
	}
}
