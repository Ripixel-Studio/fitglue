package user

import (
	"context"
	"errors"
	"testing"

	"github.com/fitglue/server/src/go/internal/infra"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/user"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type mockStore struct {
	profile *pbuser.UserProfile
	err     error
}

func (m *mockStore) GetProfile(ctx context.Context, userID string) (*pbuser.UserProfile, error) {
	return m.profile, m.err
}

func (m *mockStore) UpdateProfile(ctx context.Context, userID string, profile *pbuser.UserProfile) error {
	m.profile = profile
	return m.err
}

func (m *mockStore) FindUserByIntegration(ctx context.Context, provider, providerUID string) (*pbuser.UserProfile, error) {
	return m.profile, m.err
}

func (m *mockStore) CreateUser(ctx context.Context, userID string) (*pbuser.UserProfile, error) {
	return m.profile, m.err
}

func (m *mockStore) GetBoosterData(ctx context.Context, userID, boosterID string) (map[string]*structpb.Struct, error) {
	if m.err != nil {
		return nil, m.err
	}
	data := make(map[string]*structpb.Struct)
	if boosterID == "test_booster" {
		data["test_booster"] = &structpb.Struct{}
	}
	return data, nil
}

func (m *mockStore) SetBoosterData(ctx context.Context, userID, boosterID string, data *structpb.Struct) error {
	return m.err
}

func (m *mockStore) DeleteBoosterData(ctx context.Context, userID, boosterID string) error {
	return m.err
}

func (m *mockStore) GetIntegrations(ctx context.Context, userID string) (*pbuser.UserIntegrations, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &pbuser.UserIntegrations{}, nil
}

func (m *mockStore) SetIntegration(ctx context.Context, userID string, provider string, data interface{}) error {
	return m.err
}

func (m *mockStore) DeleteIntegration(ctx context.Context, userID string, provider string) error {
	return m.err
}

func (m *mockStore) ListCounters(ctx context.Context, userID string) ([]*pbuser.Counter, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []*pbuser.Counter{}, nil
}

func (m *mockStore) UpdateCounter(ctx context.Context, userID string, counterID string, value int64) (*pbuser.Counter, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &pbuser.Counter{Id: counterID, Count: value}, nil
}

func (m *mockStore) DeleteCounter(ctx context.Context, userID, counterID string) error {
	return m.err
}

func (m *mockStore) ListPersonalRecords(ctx context.Context, userID string) ([]*pbuser.PersonalRecord, error) {
	if m.err != nil {
		return nil, m.err
	}
	return []*pbuser.PersonalRecord{}, nil
}

func (m *mockStore) SetPersonalRecord(ctx context.Context, userID, recordType string, record *pbuser.PersonalRecord) error {
	return m.err
}

func (m *mockStore) DeletePersonalRecord(ctx context.Context, userID, recordType string) error {
	return m.err
}

func (m *mockStore) ListPluginDefaults(ctx context.Context, userID string) (map[string]*structpb.Struct, error) {
	if m.err != nil {
		return nil, m.err
	}
	return make(map[string]*structpb.Struct), nil
}

func (m *mockStore) SetPluginDefaults(ctx context.Context, userID, pluginID string, defaults *structpb.Struct) error {
	return m.err
}

func (m *mockStore) DeletePluginDefaults(ctx context.Context, userID, pluginID string) error {
	return m.err
}

type mockLogger struct{}

func (m mockLogger) Debug(ctx context.Context, msg string, args ...any) {}
func (m mockLogger) Info(ctx context.Context, msg string, args ...any)  {}
func (m mockLogger) Warn(ctx context.Context, msg string, args ...any)  {}
func (m mockLogger) Error(ctx context.Context, msg string, args ...any) {}
func (m mockLogger) With(args ...any) infra.Logger                      { return m }

func setupTest() (*Service, *mockStore) {
	store := &mockStore{}
	logger := mockLogger{}
	return NewService(store, logger), store
}

func TestGetProfile(t *testing.T) {
	svc, store := setupTest()

	t.Run("EmptyUserId", func(t *testing.T) {
		req := &pbsvc.GetProfileRequest{}
		_, err := svc.GetProfile(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.GetProfileRequest{UserId: "test_user_123"}
		_, err := svc.GetProfile(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("NotFound", func(t *testing.T) {
		store.profile = nil
		req := &pbsvc.GetProfileRequest{UserId: "test_user_123"}
		_, err := svc.GetProfile(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("Success", func(t *testing.T) {
		store.profile = &pbuser.UserProfile{
			UserId: "test_user_123",
		}
		req := &pbsvc.GetProfileRequest{UserId: "test_user_123"}
		resp, err := svc.GetProfile(context.Background(), req)
		assert.NoError(t, err)
		assert.Equal(t, "test_user_123", resp.UserId)
	})
}

func TestUpdateProfile(t *testing.T) {
	svc, store := setupTest()

	t.Run("EmptyUserId", func(t *testing.T) {
		req := &pbsvc.UpdateProfileRequest{}
		_, err := svc.UpdateProfile(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("NilProfile", func(t *testing.T) {
		req := &pbsvc.UpdateProfileRequest{UserId: "user123"}
		_, err := svc.UpdateProfile(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.UpdateProfileRequest{
			UserId:  "user123",
			Profile: &pbuser.UserProfile{},
		}
		_, err := svc.UpdateProfile(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("Success", func(t *testing.T) {
		req := &pbsvc.UpdateProfileRequest{
			UserId: "user123",
			Profile: &pbuser.UserProfile{
				UserId: "user123",
			},
		}
		resp, err := svc.UpdateProfile(context.Background(), req)
		assert.NoError(t, err)
		assert.Equal(t, "user123", resp.UserId)
		assert.Equal(t, "user123", store.profile.UserId)
	})
}

func TestCreateUser(t *testing.T) {
	svc, store := setupTest()

	t.Run("EmptyUserId", func(t *testing.T) {
		req := &pbsvc.CreateUserRequest{}
		_, err := svc.CreateUser(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.CreateUserRequest{UserId: "user123"}
		_, err := svc.CreateUser(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("Success", func(t *testing.T) {
		req := &pbsvc.CreateUserRequest{UserId: "user123"}
		store.profile = &pbuser.UserProfile{UserId: "user123"}
		resp, err := svc.CreateUser(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "user123", resp.UserId)
	})
}

func TestIntegrationRPCs(t *testing.T) {
	svc, store := setupTest()

	t.Run("GetIntegration_EmptyUserId", func(t *testing.T) {
		req := &pbsvc.GetIntegrationRequest{}
		_, err := svc.GetIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("GetIntegration_StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.GetIntegrationRequest{UserId: "user123"}
		_, err := svc.GetIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("GetIntegration_Success", func(t *testing.T) {
		req := &pbsvc.GetIntegrationRequest{UserId: "user123"}
		resp, err := svc.GetIntegration(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp.Integrations)
	})

	t.Run("SetIntegration_EmptyUserId", func(t *testing.T) {
		req := &pbsvc.SetIntegrationRequest{}
		_, err := svc.SetIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetIntegration_EmptyProvider", func(t *testing.T) {
		req := &pbsvc.SetIntegrationRequest{UserId: "user123"}
		_, err := svc.SetIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetIntegration_NilData", func(t *testing.T) {
		req := &pbsvc.SetIntegrationRequest{UserId: "user123", Provider: "strava"}
		_, err := svc.SetIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetIntegration_StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.SetIntegrationRequest{
			UserId:          "user123",
			Provider:        "strava",
			IntegrationData: &structpb.Struct{},
		}
		_, err := svc.SetIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("SetIntegration_Success", func(t *testing.T) {
		req := &pbsvc.SetIntegrationRequest{
			UserId:          "user123",
			Provider:        "strava",
			IntegrationData: &structpb.Struct{},
		}
		_, err := svc.SetIntegration(context.Background(), req)
		assert.NoError(t, err)
	})

	t.Run("DeleteIntegration_EmptyUserId", func(t *testing.T) {
		req := &pbsvc.DeleteIntegrationRequest{}
		_, err := svc.DeleteIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("DeleteIntegration_EmptyProvider", func(t *testing.T) {
		req := &pbsvc.DeleteIntegrationRequest{UserId: "user123"}
		_, err := svc.DeleteIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("DeleteIntegration_StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.DeleteIntegrationRequest{UserId: "user123", Provider: "strava"}
		_, err := svc.DeleteIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("DeleteIntegration_Success", func(t *testing.T) {
		req := &pbsvc.DeleteIntegrationRequest{UserId: "user123", Provider: "strava"}
		_, err := svc.DeleteIntegration(context.Background(), req)
		assert.NoError(t, err)
	})

	t.Run("ListIntegrations_EmptyUserId", func(t *testing.T) {
		req := &pbsvc.ListIntegrationsRequest{}
		_, err := svc.ListIntegrations(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("ListIntegrations_StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.ListIntegrationsRequest{UserId: "user123"}
		_, err := svc.ListIntegrations(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("ListIntegrations_Success", func(t *testing.T) {
		req := &pbsvc.ListIntegrationsRequest{UserId: "user123"}
		resp, err := svc.ListIntegrations(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})
}

func TestResolveUserByIntegration(t *testing.T) {
	svc, store := setupTest()

	t.Run("EmptyFields", func(t *testing.T) {
		req := &pbsvc.ResolveUserByIntegrationRequest{}
		_, err := svc.ResolveUserByIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("EmptyProviderUid", func(t *testing.T) {
		req := &pbsvc.ResolveUserByIntegrationRequest{Provider: "strava"}
		_, err := svc.ResolveUserByIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.ResolveUserByIntegrationRequest{Provider: "strava", ProviderUid: "uid123"}
		_, err := svc.ResolveUserByIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("NotFound", func(t *testing.T) {
		store.err = status.Error(codes.NotFound, "user not found")
		req := &pbsvc.ResolveUserByIntegrationRequest{Provider: "strava", ProviderUid: "uid123"}
		_, err := svc.ResolveUserByIntegration(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.NotFound, status.Code(err))
		store.err = nil
	})

	t.Run("Success", func(t *testing.T) {
		store.profile = &pbuser.UserProfile{UserId: "user123"}
		req := &pbsvc.ResolveUserByIntegrationRequest{Provider: "strava", ProviderUid: "uid123"}
		resp, err := svc.ResolveUserByIntegration(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "user123", resp.Profile.UserId)
	})
}

func TestCounterRPCs(t *testing.T) {
	svc, store := setupTest()

	t.Run("ListCounters_EmptyUserId", func(t *testing.T) {
		req := &pbsvc.ListCountersRequest{}
		_, err := svc.ListCounters(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("ListCounters_StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.ListCountersRequest{UserId: "user123"}
		_, err := svc.ListCounters(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("ListCounters_Success", func(t *testing.T) {
		req := &pbsvc.ListCountersRequest{UserId: "user123"}
		resp, err := svc.ListCounters(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp.Counters)
	})

	t.Run("UpdateCounter_EmptyFields", func(t *testing.T) {
		req := &pbsvc.UpdateCounterRequest{}
		_, err := svc.UpdateCounter(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("UpdateCounter_EmptyCounterId", func(t *testing.T) {
		req := &pbsvc.UpdateCounterRequest{UserId: "user123"}
		_, err := svc.UpdateCounter(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("UpdateCounter_StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.UpdateCounterRequest{UserId: "user123", CounterId: "counter1", Count: 5}
		_, err := svc.UpdateCounter(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("UpdateCounter_Success", func(t *testing.T) {
		req := &pbsvc.UpdateCounterRequest{UserId: "user123", CounterId: "counter1", Count: 5}
		resp, err := svc.UpdateCounter(context.Background(), req)
		assert.NoError(t, err)
		assert.Equal(t, int64(5), resp.Count)
	})
}

func TestBoosterDataRPCs(t *testing.T) {
	svc, store := setupTest()

	t.Run("GetBoosterData_EmptyUserId", func(t *testing.T) {
		req := &pbsvc.GetBoosterDataRequest{}
		_, err := svc.GetBoosterData(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("GetBoosterData_StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.GetBoosterDataRequest{UserId: "user123", BoosterId: "test_booster"}
		_, err := svc.GetBoosterData(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("GetBoosterData_Success", func(t *testing.T) {
		req := &pbsvc.GetBoosterDataRequest{UserId: "user123", BoosterId: "test_booster"}
		resp, err := svc.GetBoosterData(context.Background(), req)
		assert.NoError(t, err)
		assert.NotNil(t, resp.Data)
		assert.Contains(t, resp.Data, "test_booster")
	})

	t.Run("SetBoosterData_EmptyFields", func(t *testing.T) {
		req := &pbsvc.SetBoosterDataRequest{}
		_, err := svc.SetBoosterData(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetBoosterData_EmptyBoosterId", func(t *testing.T) {
		req := &pbsvc.SetBoosterDataRequest{UserId: "user123", Data: &structpb.Struct{}}
		_, err := svc.SetBoosterData(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetBoosterData_NilData", func(t *testing.T) {
		req := &pbsvc.SetBoosterDataRequest{UserId: "user123", BoosterId: "test_booster"}
		_, err := svc.SetBoosterData(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("SetBoosterData_StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.SetBoosterDataRequest{UserId: "user123", BoosterId: "test_booster", Data: &structpb.Struct{}}
		_, err := svc.SetBoosterData(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("SetBoosterData_Success", func(t *testing.T) {
		req := &pbsvc.SetBoosterDataRequest{UserId: "user123", BoosterId: "test_booster", Data: &structpb.Struct{}}
		_, err := svc.SetBoosterData(context.Background(), req)
		assert.NoError(t, err)
	})

	t.Run("DeleteBoosterData_EmptyFields", func(t *testing.T) {
		req := &pbsvc.DeleteBoosterDataRequest{}
		_, err := svc.DeleteBoosterData(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("DeleteBoosterData_EmptyBoosterId", func(t *testing.T) {
		req := &pbsvc.DeleteBoosterDataRequest{UserId: "user123"}
		_, err := svc.DeleteBoosterData(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("DeleteBoosterData_StoreError", func(t *testing.T) {
		store.err = errors.New("db error")
		req := &pbsvc.DeleteBoosterDataRequest{UserId: "user123", BoosterId: "test_booster"}
		_, err := svc.DeleteBoosterData(context.Background(), req)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
		store.err = nil
	})

	t.Run("DeleteBoosterData_Success", func(t *testing.T) {
		req := &pbsvc.DeleteBoosterDataRequest{UserId: "user123", BoosterId: "test_booster"}
		_, err := svc.DeleteBoosterData(context.Background(), req)
		assert.NoError(t, err)
	})
}
