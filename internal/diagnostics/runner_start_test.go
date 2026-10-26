package diagnostics

import "testing"

// TestRunner_StartDoesNotJoinARunningSweep — Start проверяет и запускает
// под одним замком: идущий прогон со своими опциями (а среди них может быть
// restart-тест) он не подхватывает и опций его не трогает, а RunWithStream,
// как и раньше, к нему присоединяется.
func TestRunner_StartDoesNotJoinARunningSweep(t *testing.T) {
	r := NewRunner(Deps{})
	r.status = RunStatus{Status: "running"}
	r.opts = RunOptions{IncludeRestart: true}

	events, started := r.Start(RunOptions{})
	if started || events != nil {
		t.Fatalf("a sweep in progress must not be started over or joined: started=%v, events=%v", started, events != nil)
	}
	if !r.opts.IncludeRestart || len(r.subscribers) != 0 {
		t.Fatalf("the running sweep must keep its options and gain no subscriber: %+v, %d", r.opts, len(r.subscribers))
	}
	if ch, err := r.RunWithStream(t.Context(), RunOptions{}); err != nil || ch == nil || len(r.subscribers) != 1 {
		t.Fatalf("RunWithStream still joins the running sweep: %v, %v", ch != nil, err)
	}
}
