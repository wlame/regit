package llm

import "fmt"

// generateWithRetry is the retry contract shared by both backends, extracted
// so HTTPBackend and AgentBackend cannot drift: call the backend up to
// maxAttempts times; a transport error retries with the UNCHANGED prompt
// (nothing useful to tell the model about a network hiccup), a validation
// failure retries with a strengthened prompt naming the violated constraint so
// the model can self-correct.
//
// call receives the current user prompt and returns the raw model text; label
// names the backend in the exhausted-attempts error.
func generateWithRetry(label, user string, validate func(string) error, call func(user string) (string, error)) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		raw, err := call(user)
		if err != nil {
			lastErr = err
			continue
		}
		if verr := validate(raw); verr == nil {
			return raw, nil
		} else {
			lastErr = verr
			user += "\n\nYour previous answer was invalid: " + verr.Error() +
				". Return ONLY valid JSON matching the requested schema."
		}
	}
	return "", fmt.Errorf("%s generation failed after %d attempts: %w", label, maxAttempts, lastErr)
}
