package render

// loaderDataProbe is the demonstration loader result type read by
// loaderDataComponent in the C03 tests. It exists only to exercise typed
// loader-data retrieval alongside route context and params in one component.
type loaderDataProbe struct {
	ID    int64
	Title string
}
