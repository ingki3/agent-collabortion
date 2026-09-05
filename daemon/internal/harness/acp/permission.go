package acp

// Decision is a policy's answer to session/request_permission.
type Decision struct {
	Outcome PermissionOutcome
	// OptionKind is the kind of the chosen option (recorded instead of
	// optionId — task_event permission.option_kind).
	OptionKind string
	// AllowOnceMissing is set when no kind=="allow_once" option was offered
	// and the policy fell back to reject_once (harness §4, E12-02).
	AllowOnceMissing bool
}

// DefaultPolicy implements harness §4: pick the option whose kind is
// "allow_once" — never by optionId — else reject_once, else "cancelled".
type DefaultPolicy struct{}

func (DefaultPolicy) Decide(p RequestPermissionParams) Decision {
	if o, ok := findKind(p.Options, "allow_once"); ok {
		return Decision{Outcome: PermissionOutcome{Outcome: "selected", OptionID: o.OptionID}, OptionKind: "allow_once"}
	}
	return Reject(p, true)
}

// Reject answers reject_once (else reject_always, else cancelled).
func Reject(p RequestPermissionParams, allowOnceMissing bool) Decision {
	if o, ok := findKind(p.Options, "reject_once"); ok {
		return Decision{Outcome: PermissionOutcome{Outcome: "selected", OptionID: o.OptionID}, OptionKind: "reject_once", AllowOnceMissing: allowOnceMissing}
	}
	if o, ok := findKind(p.Options, "reject_always"); ok {
		return Decision{Outcome: PermissionOutcome{Outcome: "selected", OptionID: o.OptionID}, OptionKind: "reject_always", AllowOnceMissing: allowOnceMissing}
	}
	return Decision{Outcome: PermissionOutcome{Outcome: "cancelled"}, AllowOnceMissing: allowOnceMissing}
}

func findKind(opts []PermissionOption, kind string) (PermissionOption, bool) {
	for _, o := range opts {
		if o.Kind == kind {
			return o, true
		}
	}
	return PermissionOption{}, false
}

// OptionKinds lists the kinds offered (task_event permission.options_offered).
func OptionKinds(opts []PermissionOption) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Kind)
	}
	return out
}
