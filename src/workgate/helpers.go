package workgate

import "context"

// Do runs fn once background work of priority p is allowed (it waits while the user
// plays or scrubs). A nil ctx waits indefinitely; a cancelled ctx returns its error
// without running fn. Slots are held for the duration of fn only, so fn must be short
// (one thumbnail, one waveform), never a whole queue.
func Do(ctx context.Context, p Prio, fn func()) error {
	if ctx == nil {
		ctx = context.Background()
	}
	release, err := Default().Acquire(ctx, p)
	if err != nil {
		return err
	}
	defer release()
	fn()
	return nil
}

// Call is Do for functions with a result and an error.
func Call[T any](p Prio, fn func() (T, error)) (T, error) {
	var v T
	var err error
	if e := Do(nil, p, func() { v, err = fn() }); e != nil {
		return v, e
	}
	return v, err
}
