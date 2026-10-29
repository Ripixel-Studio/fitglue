package health

import (
	"context"
	"errors"
	"testing"

	shared "github.com/fitglue/server/src/go/pkg"
	"github.com/fitglue/server/src/go/pkg/domain/user"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
)

// fakeDB implements only GetUser; any other call panics on the nil interface.
type fakeDB struct {
	shared.Database
	rec *user.Record
	err error
}

func (f *fakeDB) GetUser(ctx context.Context, id string) (*user.Record, error) {
	return f.rec, f.err
}

func TestFirestoreGrant(t *testing.T) {
	ctx := context.Background()
	gh := &pbuser.GoogleHealthIntegration{Enabled: true, RefreshToken: "r", Scope: "s1 s2"}
	g, err := FirestoreGrant(&fakeDB{rec: &user.Record{Integrations: &pbuser.UserIntegrations{Googlehealth: gh}}}, "u")(ctx)
	if err != nil || g.Scope != "s1 s2" || g.HTTP == nil {
		t.Fatalf("grant = %+v, %v", g, err)
	}

	for name, rec := range map[string]*user.Record{
		"no integrations": {},
		"no googlehealth": {Integrations: &pbuser.UserIntegrations{}},
		"disabled":        {Integrations: &pbuser.UserIntegrations{Googlehealth: &pbuser.GoogleHealthIntegration{RefreshToken: "r"}}},
		"no refresh":      {Integrations: &pbuser.UserIntegrations{Googlehealth: &pbuser.GoogleHealthIntegration{Enabled: true}}},
	} {
		if _, err := FirestoreGrant(&fakeDB{rec: rec}, "u")(ctx); !errors.Is(err, ErrNotConnected) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := FirestoreGrant(&fakeDB{err: errors.New("firestore down")}, "u")(ctx); err == nil || errors.Is(err, ErrNotConnected) {
		t.Errorf("a store failure is not 'not connected': %v", err)
	}
}
