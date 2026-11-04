package action

import (
	"net/url"
	"slices"

	"github.com/a-h/templ"
)

// ValidationError describes application-approved messages suitable for clients.
// Construct it with NewValidationError so the stored details cannot be mutated.
type ValidationError struct {
	message string
	fields  map[string][]string
}

// NewValidationError makes an independent snapshot of validation details.
func NewValidationError(message string, fields map[string][]string) *ValidationError {
	copied := make(map[string][]string, len(fields))
	for k, v := range fields {
		copied[k] = slices.Clone(v)
	}
	return &ValidationError{message: message, fields: copied}
}
func (*ValidationError) Error() string        { return "validation failed" }
func (*ValidationError) Is(target error) bool { return target == ErrInvalidInput }

// Message returns the optional form-level message.
func (e *ValidationError) Message() string { return e.message }

// Fields returns an independent snapshot of the field messages.
func (e *ValidationError) Fields() map[string][]string {
	copied := make(map[string][]string, len(e.fields))
	for k, v := range e.fields {
		copied[k] = slices.Clone(v)
	}
	return copied
}

// FormState holds request-local, explicitly allowed submitted values and errors.
type FormState struct {
	values     url.Values
	validation *ValidationError
}

// NewFormState copies only allowed fields; framework CSRF values are never included.
func NewFormState(values url.Values, allowed []string, validation *ValidationError) FormState {
	out := make(url.Values)
	for _, name := range allowed {
		if name != csrfFieldName {
			if v, ok := values[name]; ok {
				out[name] = slices.Clone(v)
			}
		}
	}
	if validation == nil {
		return FormState{values: out}
	}
	return FormState{values: out, validation: NewValidationError(validation.Message(), validation.Fields())}
}

// Values returns a copy of all submitted values for an allowed field.
func (s FormState) Values(name string) []string { return slices.Clone(s.values[name]) }

// Value returns the first submitted value for an allowed field.
func (s FormState) Value(name string) string { return s.values.Get(name) }

// Errors returns a copy of the messages for a field.
func (s FormState) Errors(name string) []string {
	if s.validation == nil {
		return nil
	}
	return slices.Clone(s.validation.fields[name])
}

// Message returns the optional form-level message.
func (s FormState) Message() string {
	if s.validation == nil {
		return ""
	}
	return s.validation.Message()
}

// FormRenderer renders a complete feedback document for one action.
type FormRenderer func(FormState) templ.Component

// WithFormFeedback attaches a feedback renderer and an explicit repopulation allowlist.
func (b Binding) WithFormFeedback(allowed []string, renderer FormRenderer) Binding {
	b.formFields = slices.Clone(allowed)
	b.formRenderer = renderer
	return b
}

// FormFeedback constructs a renderer using only explicitly allowed values.
func (b Binding) FormFeedback(values url.Values, validation *ValidationError) templ.Component {
	if b.formRenderer == nil {
		return nil
	}
	return b.formRenderer(NewFormState(values, b.formFields, validation))
}
