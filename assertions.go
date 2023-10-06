package slogassert

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"time"
)

const (
	// LevelDontCare can be used in a LogMessageMatch to indicate
	// that the level does not need to match.
	LevelDontCare = slog.Level(-255000000)
)

// AssertEmpty asserts that all log messages have now been accounted
// for and there is nothing left.
//
// A call to this method will be automatically deferred through the
// testing system if you use New(), but you can also use New
func (h *Handler) AssertEmpty() {
	h = h.root()
	h.m.Lock()
	defer h.m.Unlock()

	if len(h.logMessages) == 0 {
		return
	}

	// If this is used in a test as defer handler.AssertEmpty(),
	// this validates that we're not currently in a panic
	// recovery. If we are, we let the panic through rather than
	// calling h.t.Fatalf, due to:
	//
	// https://github.com/golang/go/issues/49929
	//
	// If that is ever fixed we can resume the original API that
	// uses t.Cleanup to automatically clean up, but otherwise
	// eating panics has proved to be too confusing and it's
	// better to just ask people to defer handler.Cleanup if they
	// want that behavior.
	r := recover()
	if r == nil {
		for _, lm := range h.logMessages {
			lm.Print(os.Stderr)
		}

		h.t.Fatalf("%d unasserted log message(s); see printout above",
			len(h.logMessages))
	} else {
		panic(r)
	}
}

// AssertSomeMessage asserts that some logging events were recorded
// with the given message. The return value is the number of matched
// messages if there were any. (If there aren't any this fails the test.)
func (h *Handler) AssertSomeMessage(msg string) int {
	matches := h.filter(func(lm LogMessage) (bool, bool) {
		return lm.Message == msg, true
	})
	if matches == 0 {
		h.t.Fatalf("No logs with message %q found", msg)
	}
	return matches
}

// AssertMessage asserts a logging message recorded with the giving
// logging message.
func (h *Handler) AssertMessage(msg string) {
	matches := h.filter(func(lm LogMessage) (bool, bool) {
		match := lm.Message == msg
		return match, !match
	})
	if matches == 0 {
		h.t.Fatalf("No logs with message %q found", msg)
	}
}

// AssertSomeMessageLevel is a weak assertion that asserts that some
// logging events were recorded with the given message, at the given
// logging level. The return value is the number of matched messages
// if there were any. (If there aren't any this fails the test.)
func (h *Handler) AssertSomeMessageLevel(msg string, level slog.Level) int {
	matches := h.filter(func(lm LogMessage) (bool, bool) {
		return lm.Message == msg && lm.Level == level, true
	})
	if matches == 0 {
		h.t.Fatalf("No logs with message %q and level %s found", msg, level)
	}
	return matches
}

// AssertMessageLevel asserts a logging message with the given message
// and level.
func (h *Handler) AssertMessageLevel(msg string, level slog.Level) {
	matches := h.filter(func(lm LogMessage) (bool, bool) {
		matches := lm.Message == msg && lm.Level == level
		return matches, !matches
	})
	if matches == 0 {
		h.t.Fatalf("No logs with message %q and level %s found", msg, level)
	}
}

// AssertPrecise takes a LogMessageMatch and asserts the first log
// message that matches it.
func (h *Handler) AssertPrecise(lmm LogMessageMatch) {
	matches := h.filter(func(lm LogMessage) (bool, bool) {
		matches := lmm.matches(lm)
		return matches, !matches
	})
	if matches == 0 {
		h.t.Fatal("No logs matching filter were found")
	}
}

// AssertSomePrecise asserts all the messages in the log that match
// the LogMessageMatch criteria. The return value is th enumber of
// matched messages if there were any. (If there aren't any this fails
// the test.)
func (h *Handler) AssertSomePrecise(lmm LogMessageMatch) int {
	matches := h.filter(func(lm LogMessage) (bool, bool) {
		return lmm.matches(lm), true
	})
	if matches == 0 {
		h.t.Fatal("No logs matching filter were found")
	}
	return matches
}

// LogMessageMatch defines a precise message to match.
//
// The Message works as you'd expect; an equality check. It is always
// checked, so an empty message means to verify that the message
// logged was empty.
//
// If Level is LevelDontCare, the level won't be matched. Otherwise,
// it will also be an equality check.
//
// Attrs is a map of string to any. The strings will be the groups for
// the given attribute, joined together by dots. For instance, an
// ungrouped key called "url" will be "url". If it is in a "request"
// group, it will be keyed by "request.url". If that is also in a
// "webserver" group, the key will be "webserver.request.url". Any
// dots in the keys themselves will be backslash encoded, so a
// top-level key called "a.b" will be "a\.b" in this map.
//
// The value is a matcher on the attribute, which may be one of three
// things.
//
// It can be a function "func (slog.Value) bool", which will be passed
// the value. If it returns true, it is considered to match; false is
// considered to be not a match.
//
// It can be a function "func (T) bool", where "T" matches the
// concrete value behind the Kind of the slog.Value. In that case, the
// same rules apply. For KindAny, this must be precisely "func(any)
// bool"; this is done via type switching, not a lot of `reflect`
// calls, so only and exactly "func (any) bool" will work.
//
// It can be a concrete value, in which case it must be equal to the
// value contained in the attribute. Type-appropriate equality is
// used, e.g., time.Time's are compared via time.Equal.
//
// Any other value will result in an error being returned when used to
// match.
//
// AllAttrsMatch indicate whether the Attrs map must contain matches
// for all attributes in the match. If true, and there are unmatched
// attribtues in the log message, the match will fail. If false, extra
// attributes in the log message won't fail the match.
type LogMessageMatch struct {
	Message       string
	Level         slog.Level
	Attrs         map[string]any
	AllAttrsMatch bool
}

func (lmm LogMessageMatch) matches(lm LogMessage) bool {
	if lmm.Message != lm.Message {
		return false
	}
	if lmm.Level != LevelDontCare && lmm.Level != lm.Level {
		return false
	}

	for key, matcher := range lmm.Attrs {
		val, haveVal := lm.Attrs[key]
		if !haveVal {
			// mandatory attribute missing
			return false
		}
		if matchAttr(matcher, val) != nil {
			return false
		}
	}

	if lmm.AllAttrsMatch && len(lmm.Attrs) != len(lm.Attrs) {
		return false
	}

	return true
}

// Unasserted returns all the log messages that are currently
// unasserted within the slog assert. The returned result is a deep
// copy. This method does NOT assert them; after a call to this
// method, if there are any messages an AssertEmpty will still fail.
//
// It is probably superficially tempting to just use this and examine
// the result with code. However, bear in mind that using the
// assertion functions in conjuction with the default AssertEmpty on
// test cleanup already handles making sure everything is
// asserted. There's a lot of bugs easy to write with direct code
// examination.
//
// However, sometimes you just need to check the messages with code.
func (h *Handler) Unasserted() []LogMessage {
	msgs := []LogMessage{}
	root := h.root()
	root.m.Lock()
	defer root.m.Unlock()

	for _, msg := range root.logMessages {
		msgs = append(msgs, msg.clone())
	}
	return msgs
}

// Reset will simply empty out the log entirely. This can be used in
// anger to simply make tests pass, or when you legitimately have some
// logging messages you don't want to bind your tests to (for instance
// this package's own call to testing/slogtest).
func (h *Handler) Reset() {
	root := h.root()
	root.m.Lock()
	root.logMessages = nil
	root.m.Unlock()
}

// filter removes from our logMessages anything that returns true.
//
// The returned int is the number of matches that occurred.
func (h *Handler) filter(f func(LogMessage) (bool, bool)) int {
	root := h.root()
	root.m.Lock()
	defer root.m.Unlock()
	newMessages := []LogMessage{}

	matchCount := 0
	for idx, lm := range root.logMessages {
		matched, keepMatching := f(lm)
		if matched {
			matchCount++
		} else {
			newMessages = append(newMessages, lm)
		}

		if !keepMatching {
			// save off the rest of the messages
			newMessages = append(newMessages, root.logMessages[idx+1:]...)
			break
		}
	}

	root.logMessages = newMessages

	return matchCount
}

// return when the types are correct, and it just doesn't match.
var errNoMatch = errors.New("does not match")

func matchAttr(matcher any, val slog.Value) error {
	switch val.Kind() {
	case slog.KindAny:
		switch match := matcher.(type) {
		case func(slog.Value) bool:
			if match(val) {
				return nil
			}
			return errNoMatch
		case func(any) bool:
			if match(val.Any()) {
				return nil
			}
			return errNoMatch
		case any:
			if reflect.DeepEqual(match, val.Any()) {
				return nil
			}
			return errNoMatch
		default:
			// this can't happen but the compiler can't prove it.
			return fmt.Errorf("invalid type for comparing KindAny: %T", matcher)
		}

	case slog.KindBool:
		switch match := matcher.(type) {
		case func(slog.Value) bool:
			if match(val) {
				return nil
			}
			return errNoMatch
		case func(bool) bool:
			if match(val.Bool()) {
				return nil
			}
			return errNoMatch
		case bool:
			if match == val.Bool() {
				return nil
			}
			return errNoMatch
		default:
			return fmt.Errorf("invalid type for comparing KindBool: %T", matcher)
		}

	case slog.KindDuration:
		switch match := matcher.(type) {
		case func(slog.Value) bool:
			if match(val) {
				return nil
			}
			return errNoMatch
		case func(time.Duration) bool:
			if match(val.Duration()) {
				return nil
			}
			return errNoMatch
		case time.Duration:
			if match == val.Duration() {
				return nil
			}
			return errNoMatch
		default:
			return fmt.Errorf("invalid type for comparing KindDuration: %T", matcher)
		}

	case slog.KindFloat64:
		switch match := matcher.(type) {
		case func(slog.Value) bool:
			if match(val) {
				return nil
			}
			return errNoMatch
		case func(float64) bool:
			if match(val.Float64()) {
				return nil
			}
			return errNoMatch
		case float64:
			if match == val.Float64() {
				return nil
			}
			return errNoMatch
		case int:
			if float64(match) == val.Float64() {
				return nil
			}
			return errNoMatch
		default:
			return fmt.Errorf("invalid type for comparing KindFloat64: %T", matcher)
		}

	case slog.KindInt64:
		switch match := matcher.(type) {
		case func(slog.Value) bool:
			if match(val) {
				return nil
			}
			return errNoMatch
		case func(int64) bool:
			if match(val.Int64()) {
				return nil
			}
			return errNoMatch
		case int64:
			if match == val.Int64() {
				return nil
			}
			return errNoMatch
		case int:
			if int64(match) == val.Int64() {
				return nil
			}
			return errNoMatch
		default:
			return fmt.Errorf("invalid type for comparing KindInt64: %T", matcher)
		}

	case slog.KindString:
		switch match := matcher.(type) {
		case func(slog.Value) bool:
			if match(val) {
				return nil
			}
			return errNoMatch
		case func(string) bool:
			if match(val.String()) {
				return nil
			}
			return errNoMatch
		case string:
			if match == val.String() {
				return nil
			}
			return errNoMatch
		default:
			return fmt.Errorf("invalid type for comparing KindString: %T", matcher)
		}

	case slog.KindTime:
		switch match := matcher.(type) {
		case func(slog.Value) bool:
			if match(val) {
				return nil
			}
			return errNoMatch
		case func(time.Time) bool:
			if match(val.Time()) {
				return nil
			}
			return errNoMatch
		case time.Time:
			if match.Equal(val.Time()) {
				return nil
			}
			return errNoMatch
		default:
			return fmt.Errorf("invalid type for comparing KindTime: %T", matcher)
		}

	case slog.KindUint64:
		switch match := matcher.(type) {
		case func(slog.Value) bool:
			if match(val) {
				return nil
			}
			return errNoMatch
		case func(uint64) bool:
			if match(val.Uint64()) {
				return nil
			}
			return errNoMatch
		case uint64:
			if match == val.Uint64() {
				return nil
			}
			return errNoMatch
		case int:
			if uint64(match) == val.Uint64() {
				return nil
			}
			return errNoMatch
		default:
			return fmt.Errorf("invalid type for comparing KindUint64: %T", matcher)
		}

	case slog.KindLogValuer:
		return matchAttr(matcher, val.LogValuer().LogValue())

	default:
		// This means slog has apparently added a type this code is
		// not familiar with and an Issue needs to be raised on
		// Github with the Kind in question. Just this panic message
		// should be enough to diagnose the issue.
		panic(fmt.Sprintf("unknown kind in slog: %s", val.Kind()))
	}
}
