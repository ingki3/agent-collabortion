package acpprobe

// PermissionPolicy decides how to answer session/request_permission.
type PermissionPolicy interface {
	Decide(RequestPermissionParams) Decision
}

// Decision is a policy's answer.
type Decision struct {
	Outcome PermissionOutcome
	// AllowOnceMissing is set when no kind=="allow_once" option was offered
	// and the policy fell back to reject_once (PRD §8.2.2, EVAL E12-02).
	AllowOnceMissing bool
}

// DefaultPolicy implements PRD §8.2.2: pick the option whose kind is
// "allow_once" — never by optionId — else reject_once, else "cancelled".
type DefaultPolicy struct{}

func (DefaultPolicy) Decide(p RequestPermissionParams) Decision {
	if o, ok := findKind(p.Options, "allow_once"); ok {
		return Decision{Outcome: PermissionOutcome{Outcome: "selected", OptionID: o.OptionID}}
	}
	if o, ok := findKind(p.Options, "reject_once"); ok {
		return Decision{Outcome: PermissionOutcome{Outcome: "selected", OptionID: o.OptionID}, AllowOnceMissing: true}
	}
	return Decision{Outcome: PermissionOutcome{Outcome: "cancelled"}, AllowOnceMissing: true}
}

// RejectAllPolicy answers reject_once to everything (used to observe how the
// agent reacts to a refused tool call).
type RejectAllPolicy struct{}

func (RejectAllPolicy) Decide(p RequestPermissionParams) Decision {
	if o, ok := findKind(p.Options, "reject_once"); ok {
		return Decision{Outcome: PermissionOutcome{Outcome: "selected", OptionID: o.OptionID}}
	}
	return Decision{Outcome: PermissionOutcome{Outcome: "cancelled"}}
}

func findKind(opts []PermissionOption, kind string) (PermissionOption, bool) {
	for _, o := range opts {
		if o.Kind == kind {
			return o, true
		}
	}
	return PermissionOption{}, false
}
