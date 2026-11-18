package localqueue

import (
	"net/http"
	"time"
)

// Policy is one subscription's delivery rules, copied from the push
// subscriptions in terraform/pubsub.tf so behaviour is unchanged.
type Policy struct {
	Subscription string        // the Pub/Sub subscription it replaces (envelope + logs)
	Topic        string        // topic it consumes
	MinBackoff   time.Duration // retry_policy.minimum_backoff
	MaxBackoff   time.Duration // retry_policy.maximum_backoff
	MaxAttempts  int           // dead_letter_policy.max_delivery_attempts
	AckDeadline  time.Duration // ack_deadline_seconds: the delivery lease
	Workers      int           // concurrent deliveries
}

// noDeadLetter stands in for Pub/Sub's message retention on subscriptions
// without a dead-letter policy: at the 10-minute maximum backoff, about a day
// of retries before the message is parked as dead (still visible, still
// retryable with `fitgluectl queue retry`).
const noDeadLetter = 150

// The subscriptions the box runs, by consuming service.
var (
	SubPipelineRaw = Policy{Subscription: "sub-pipeline-raw", Topic: "topic-raw-activity",
		MinBackoff: 10 * time.Second, MaxBackoff: 600 * time.Second, MaxAttempts: noDeadLetter, AckDeadline: 600 * time.Second, Workers: 2}
	SubPipelineRun = Policy{Subscription: "sub-pipeline-run", Topic: "topic-pipeline-activity",
		MinBackoff: 30 * time.Second, MaxBackoff: 600 * time.Second, MaxAttempts: 5, AckDeadline: 600 * time.Second, Workers: 4}
	SubEnrichmentLag = Policy{Subscription: "sub-enrichment-lag", Topic: "topic-enrichment-lag",
		MinBackoff: 60 * time.Second, MaxBackoff: 600 * time.Second, MaxAttempts: 100, AckDeadline: 600 * time.Second, Workers: 2}
	SubPipelineEnriched = Policy{Subscription: "sub-pipeline-enriched", Topic: "topic-enriched-activity",
		MinBackoff: 10 * time.Second, MaxBackoff: 600 * time.Second, MaxAttempts: noDeadLetter, AckDeadline: 600 * time.Second, Workers: 2}
	SubDestinationUpload = Policy{Subscription: "sub-destination-upload", Topic: "topic-destination-upload",
		MinBackoff: 10 * time.Second, MaxBackoff: 600 * time.Second, MaxAttempts: noDeadLetter, AckDeadline: 600 * time.Second, Workers: 4}
	SubRoundupTrigger = Policy{Subscription: "sub-roundup-trigger", Topic: "topic-roundup-trigger",
		MinBackoff: 60 * time.Second, MaxBackoff: 600 * time.Second, MaxAttempts: noDeadLetter, AckDeadline: 600 * time.Second, Workers: 1}
)

// AllPolicies lists every subscription; a topic not consumed by one of them
// has no subscriber and its messages are dropped at publish.
var AllPolicies = []Policy{SubPipelineRaw, SubPipelineRun, SubEnrichmentLag, SubPipelineEnriched, SubDestinationUpload, SubRoundupTrigger}

// KnownTopic reports whether some subscription consumes topic.
func KnownTopic(topic string) bool {
	for _, p := range AllPolicies {
		if p.Topic == topic {
			return true
		}
	}
	return false
}

// Subscription binds a policy to the push handler that used to receive it.
type Subscription struct {
	Policy
	Handler http.Handler
}

// Backoff is the delay before redelivery after the given (1-based) attempt:
// MinBackoff doubled per attempt, capped at MaxBackoff.
func (p Policy) Backoff(attempt int) time.Duration {
	d := p.MinBackoff
	for i := 1; i < attempt && d < p.MaxBackoff; i++ {
		d *= 2
	}
	if d > p.MaxBackoff {
		d = p.MaxBackoff
	}
	return d
}
