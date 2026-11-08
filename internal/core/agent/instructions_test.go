package agent

import (
	"errors"
	"testing"
)

func TestFindBlock(t *testing.T) {
	text := "above\r\n" + InstructionBegin + "\r\nbody\r\n" + InstructionEnd + "\r\nbelow\n"
	start, end, found, err := FindBlock([]byte(text))
	if err != nil || !found || text[start:end] != InstructionBegin+"\r\nbody\r\n"+InstructionEnd+"\r\n" {
		t.Fatalf("%d %d %t %v", start, end, found, err)
	}
	if _, _, found, err := FindBlock([]byte("# plain\n")); found || err != nil {
		t.Fatalf("found=%t err=%v", found, err)
	}
	for _, bad := range []string{InstructionBegin + "\n", InstructionEnd + "\n", InstructionEnd + "\n" + InstructionBegin + "\n", Block + Block} {
		if _, _, _, err := FindBlock([]byte(bad)); !errors.Is(err, ErrBlockMarkers) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

func TestBlockCurrent(t *testing.T) {
	for text, want := range map[string][2]bool{
		"":                   {false, false},
		"# x\n" + Block:      {true, true},
		Block[:len(Block)-1]: {true, true},
		InstructionBegin + "\nold\n" + InstructionEnd + "\n": {false, true},
	} {
		current, present, err := BlockCurrent([]byte(text))
		if err != nil || current != want[0] || present != want[1] {
			t.Fatalf("%q: %t %t %v", text, current, present, err)
		}
	}
}

func TestBlockForMCPAddsLine(t *testing.T) {
	got := BlockFor(true)
	want := InstructionBegin + "\n" + InstallOffer + "\n" + UsageNote + "\n" + MCPNote + "\n" + InstructionEnd + "\n"
	if got != want {
		t.Fatalf("want %q got %q", want, got)
	}
}

func TestBlockForNoMCPIsBlock(t *testing.T) {
	if BlockFor(false) != Block {
		t.Fatalf("BlockFor(false) = %q", BlockFor(false))
	}
}

func TestBlockCurrentForOldBlockIsStale(t *testing.T) {
	current, present, err := BlockCurrentFor([]byte("# x\n"+Block), true)
	if err != nil || current || !present {
		t.Fatalf("old block against MCP: current=%t present=%t err=%v", current, present, err)
	}
	current, present, err = BlockCurrentFor([]byte("# x\n"+BlockFor(true)), true)
	if err != nil || !current || !present {
		t.Fatalf("MCP block: current=%t present=%t err=%v", current, present, err)
	}
	if current, _, _ := BlockCurrentFor([]byte(BlockFor(true)), false); current {
		t.Fatal("MCP block is current without MCP")
	}
	if current, _, _ := BlockCurrent([]byte(Block)); !current {
		t.Fatal("BlockCurrent changed")
	}
}
