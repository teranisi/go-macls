package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

// terminalStateMu guards every field below: whichever raw-mode terminal
// state is currently active (waitForContinuePlain()/waitForContinueClick()'s
// own term.MakeRaw(), tracked here via enterRawMode()/exitRawMode() instead
// of a bare local variable), whether the cursor is currently hidden
// (DECTCEM, see renderProgressiveImages()/renderProgressiveMultiImages()),
// and whether mouse click reporting is currently on (see preview.go) --
// so installSignalCleanup()'s handler can undo all of it if the process is
// killed by a signal instead of exiting normally, a path none of the
// ordinary defers protecting these ever runs for (see its own doc comment).
var (
	terminalStateMu sync.Mutex
	rawModeFD       = -1
	rawModeState    *term.State
	cursorHidden    bool
	mouseOn         bool
)

// enterRawMode is term.MakeRaw(), plus recording the resulting state so a
// signal received while raw mode is active can still be restored from (see
// installSignalCleanup()).
func enterRawMode(fd int) (*term.State, error) {
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	terminalStateMu.Lock()
	rawModeFD, rawModeState = fd, oldState
	terminalStateMu.Unlock()
	return oldState, nil
}

// exitRawMode is enterRawMode()'s counterpart -- always call in a defer
// right after a successful enterRawMode(), exactly like term.Restore()
// itself.
func exitRawMode(fd int, oldState *term.State) {
	term.Restore(fd, oldState)
	terminalStateMu.Lock()
	rawModeFD, rawModeState = -1, nil
	terminalStateMu.Unlock()
}

func setCursorHidden(hidden bool) {
	terminalStateMu.Lock()
	cursorHidden = hidden
	terminalStateMu.Unlock()
}

func setMouseTrackingOn(on bool) {
	terminalStateMu.Lock()
	mouseOn = on
	terminalStateMu.Unlock()
}

// installSignalCleanup registers a handler for SIGINT/SIGTERM that restores
// the terminal to a normal, usable state -- showing the cursor again,
// turning off mouse click reporting, and restoring cooked mode if raw mode
// was active -- before the process actually exits.
//
// Without this, a signal arriving while a thumbnail is mid-draw (the
// cursor hidden per renderProgressiveImages()/renderProgressiveMultiImages(),
// but not necessarily inside --paging's own raw-mode prompt, so it's never
// one of the keypresses waitForContinue() itself recognizes as "quit")
// skips every deferred cleanup entirely: Go's default disposition for an
// unhandled SIGINT/SIGTERM is to terminate the process immediately, not to
// unwind and run pending defers. Left alone, that leaves the terminal with
// an invisible cursor, stray mouse-report escape codes on every future
// keystroke, or both, until the user runs `reset` or opens a new tab.
//
// Call once, as early as possible (see main()).
func installSignalCleanup() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-ch
		terminalStateMu.Lock()
		if cursorHidden {
			fmt.Print("\033[?25h")
		}
		if mouseOn {
			fmt.Print(mouseTrackingDisable)
		}
		if rawModeState != nil {
			term.Restore(rawModeFD, rawModeState)
		}
		terminalStateMu.Unlock()
		if sig == syscall.SIGTERM {
			os.Exit(143) // 128 + SIGTERM
		}
		os.Exit(130) // 128 + SIGINT
	}()
}
