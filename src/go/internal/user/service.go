package user

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/fitglue/server/src/go/internal/infra"
	pbuser "github.com/fitglue/server/src/go/pkg/types/pb/models/user"
	pbsvc "github.com/fitglue/server/src/go/pkg/types/pb/services/user"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type Service struct {
	pbsvc.UnimplementedUserServiceServer
	store  Store
	logger infra.Logger
}

func NewService(store Store, logger infra.Logger) *Service {
	return &Service{
		store:  store,
		logger: logger,
	}
}

func (s *Service) GetProfile(ctx context.Context, req *pbsvc.GetProfileRequest) (*pbuser.UserProfile, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	profile, err := s.store.GetProfile(ctx, req.UserId)
	if err != nil {
		s.logger.Error(ctx, "failed to get profile", "err", err, "user_id", req.UserId)
		return nil, status.Error(codes.Internal, "failed to get profile")
	}
	if profile == nil {
		return nil, status.Error(codes.NotFound, "profile not found")
	}

	return profile, nil
}

func (s *Service) UpdateProfile(ctx context.Context, req *pbsvc.UpdateProfileRequest) (*pbuser.UserProfile, error) {
	if req.UserId == "" || req.Profile == nil {
		return nil, status.Error(codes.InvalidArgument, "user_id and profile are required")
	}

	err := s.store.UpdateProfile(ctx, req.UserId, req.Profile)
	if err != nil {
		s.logger.Error(ctx, "failed to update profile", "err", err, "user_id", req.UserId)
		return nil, status.Error(codes.Internal, "failed to update profile")
	}

	return req.Profile, nil
}

func (s *Service) GetIntegration(ctx context.Context, req *pbsvc.GetIntegrationRequest) (*pbsvc.GetIntegrationResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	integrations, err := s.store.GetIntegrations(ctx, req.UserId)
	if err != nil {
		s.logger.Error(ctx, "failed to get integrations", "err", err, "user_id", req.UserId)
		return nil, status.Error(codes.Internal, "failed to get integrations")
	}

	return &pbsvc.GetIntegrationResponse{Integrations: integrations}, nil
}

func (s *Service) ResolveUserByIntegration(ctx context.Context, req *pbsvc.ResolveUserByIntegrationRequest) (*pbsvc.ResolveUserByIntegrationResponse, error) {
	if req.Provider == "" || req.ProviderUid == "" {
		return nil, status.Error(codes.InvalidArgument, "provider and provider_uid are required")
	}

	profile, err := s.store.FindUserByIntegration(ctx, req.Provider, req.ProviderUid)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, err
		}
		s.logger.Error(ctx, "failed to resolve user by integration", "err", err, "provider", req.Provider, "provider_uid", req.ProviderUid)
		return nil, status.Error(codes.Internal, "failed to resolve user by integration")
	}

	return &pbsvc.ResolveUserByIntegrationResponse{Profile: profile}, nil
}

func (s *Service) SetIntegration(ctx context.Context, req *pbsvc.SetIntegrationRequest) (*emptypb.Empty, error) {
	if req.UserId == "" || req.Provider == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and provider are required")
	}
	if req.IntegrationData == nil {
		return nil, status.Error(codes.InvalidArgument, "integration_data is required")
	}

	err := s.store.SetIntegration(ctx, req.UserId, req.Provider, req.IntegrationData.AsMap())
	if err != nil {
		s.logger.Error(ctx, "failed to set integration", "err", err, "user_id", req.UserId, "provider", req.Provider)
		return nil, status.Error(codes.Internal, "failed to set integration")
	}

	return &emptypb.Empty{}, nil
}

func (s *Service) DeleteIntegration(ctx context.Context, req *pbsvc.DeleteIntegrationRequest) (*emptypb.Empty, error) {
	if req.UserId == "" || req.Provider == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and provider are required")
	}

	// Best-effort token revocation before removing credentials from the store.
	if req.Provider == "strava" {
		integ, err := s.store.GetIntegrations(ctx, req.UserId)
		if err == nil && integ.GetStrava().GetAccessToken() != "" {
			if rErr := revokeStravaToken(ctx, integ.GetStrava().GetAccessToken()); rErr != nil {
				s.logger.Error(ctx, "failed to revoke strava token", "err", rErr, "user_id", req.UserId)
			}
		}
	}

	err := s.store.DeleteIntegration(ctx, req.UserId, req.Provider)
	if err != nil {
		s.logger.Error(ctx, "failed to delete integration", "err", err, "user_id", req.UserId, "provider", req.Provider)
		return nil, status.Error(codes.Internal, "failed to delete integration")
	}

	return &emptypb.Empty{}, nil
}

func revokeStravaToken(ctx context.Context, accessToken string) error {
	body := strings.NewReader(url.Values{"access_token": {accessToken}}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.strava.com/oauth/revoke", body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("strava revoke returned status %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) ListIntegrations(ctx context.Context, req *pbsvc.ListIntegrationsRequest) (*pbuser.UserIntegrations, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	integrations, err := s.store.GetIntegrations(ctx, req.UserId)
	if err != nil {
		s.logger.Error(ctx, "failed to list integrations", "err", err, "user_id", req.UserId)
		return nil, status.Error(codes.Internal, "failed to list integrations")
	}

	return integrations, nil
}

func (s *Service) ListCounters(ctx context.Context, req *pbsvc.ListCountersRequest) (*pbsvc.ListCountersResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	counters, err := s.store.ListCounters(ctx, req.UserId)
	if err != nil {
		s.logger.Error(ctx, "failed to list counters", "err", err, "user_id", req.UserId)
		return nil, status.Error(codes.Internal, "failed to list counters")
	}

	return &pbsvc.ListCountersResponse{Counters: counters}, nil
}

func (s *Service) UpdateCounter(ctx context.Context, req *pbsvc.UpdateCounterRequest) (*pbuser.Counter, error) {
	if req.UserId == "" || req.CounterId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and counter_id are required")
	}

	counter, err := s.store.UpdateCounter(ctx, req.UserId, req.CounterId, req.Count)
	if err != nil {
		s.logger.Error(ctx, "failed to update counter", "err", err, "user_id", req.UserId, "counter_id", req.CounterId)
		return nil, status.Error(codes.Internal, "failed to update counter")
	}

	return counter, nil
}

func (s *Service) GetBoosterData(ctx context.Context, req *pbsvc.GetBoosterDataRequest) (*pbsvc.GetBoosterDataResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	data, err := s.store.GetBoosterData(ctx, req.UserId, req.BoosterId)
	if err != nil {
		s.logger.Error(ctx, "failed to get booster data", "err", err, "user_id", req.UserId, "booster_id", req.BoosterId)
		return nil, status.Error(codes.Internal, "failed to get booster data")
	}

	return &pbsvc.GetBoosterDataResponse{
		Data: data,
	}, nil
}

func (s *Service) CreateUser(ctx context.Context, req *pbsvc.CreateUserRequest) (*pbuser.UserProfile, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	profile, err := s.store.CreateUser(ctx, req.UserId)
	if err != nil {
		s.logger.Error(ctx, "failed to create user", "err", err, "user_id", req.UserId)
		return nil, status.Error(codes.Internal, "failed to create user")
	}

	return profile, nil
}

func (s *Service) SetBoosterData(ctx context.Context, req *pbsvc.SetBoosterDataRequest) (*emptypb.Empty, error) {
	if req.UserId == "" || req.BoosterId == "" || req.Data == nil {
		return nil, status.Error(codes.InvalidArgument, "user_id, booster_id, and data are required")
	}

	err := s.store.SetBoosterData(ctx, req.UserId, req.BoosterId, req.Data)
	if err != nil {
		s.logger.Error(ctx, "failed to set booster data", "err", err, "user_id", req.UserId, "booster_id", req.BoosterId)
		return nil, status.Error(codes.Internal, "failed to set booster data")
	}

	return &emptypb.Empty{}, nil
}

func (s *Service) DeleteBoosterData(ctx context.Context, req *pbsvc.DeleteBoosterDataRequest) (*emptypb.Empty, error) {
	if req.UserId == "" || req.BoosterId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and booster_id are required")
	}

	err := s.store.DeleteBoosterData(ctx, req.UserId, req.BoosterId)
	if err != nil {
		s.logger.Error(ctx, "failed to delete booster data", "err", err, "user_id", req.UserId, "booster_id", req.BoosterId)
		return nil, status.Error(codes.Internal, "failed to delete booster data")
	}

	return &emptypb.Empty{}, nil
}

func (s *Service) DeleteCounter(ctx context.Context, req *pbsvc.DeleteCounterRequest) (*emptypb.Empty, error) {
	if req.UserId == "" || req.CounterId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and counter_id are required")
	}

	err := s.store.DeleteCounter(ctx, req.UserId, req.CounterId)
	if err != nil {
		s.logger.Error(ctx, "failed to delete counter", "err", err, "user_id", req.UserId, "counter_id", req.CounterId)
		return nil, status.Error(codes.Internal, "failed to delete counter")
	}

	return &emptypb.Empty{}, nil
}

func (s *Service) ListPersonalRecords(ctx context.Context, req *pbsvc.ListPersonalRecordsRequest) (*pbsvc.ListPersonalRecordsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	records, err := s.store.ListPersonalRecords(ctx, req.UserId)
	if err != nil {
		s.logger.Error(ctx, "failed to list personal records", "err", err, "user_id", req.UserId)
		return nil, status.Error(codes.Internal, "failed to list personal records")
	}

	return &pbsvc.ListPersonalRecordsResponse{Records: records}, nil
}

func (s *Service) SetPersonalRecord(ctx context.Context, req *pbsvc.SetPersonalRecordRequest) (*pbuser.PersonalRecord, error) {
	if req.UserId == "" || req.RecordType == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and record_type are required")
	}

	record := &pbuser.PersonalRecord{
		RecordType: req.RecordType,
		Value:      req.Value,
		Unit:       req.Unit,
		ActivityId: req.ActivityId,
	}

	err := s.store.SetPersonalRecord(ctx, req.UserId, req.RecordType, record)
	if err != nil {
		s.logger.Error(ctx, "failed to set personal record", "err", err, "user_id", req.UserId, "record_type", req.RecordType)
		return nil, status.Error(codes.Internal, "failed to set personal record")
	}

	return record, nil
}

func (s *Service) DeletePersonalRecord(ctx context.Context, req *pbsvc.DeletePersonalRecordRequest) (*emptypb.Empty, error) {
	if req.UserId == "" || req.RecordType == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and record_type are required")
	}

	err := s.store.DeletePersonalRecord(ctx, req.UserId, req.RecordType)
	if err != nil {
		s.logger.Error(ctx, "failed to delete personal record", "err", err, "user_id", req.UserId, "record_type", req.RecordType)
		return nil, status.Error(codes.Internal, "failed to delete personal record")
	}

	return &emptypb.Empty{}, nil
}

func (s *Service) ListPluginDefaults(ctx context.Context, req *pbsvc.ListPluginDefaultsRequest) (*pbsvc.ListPluginDefaultsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	defaults, err := s.store.ListPluginDefaults(ctx, req.UserId)
	if err != nil {
		s.logger.Error(ctx, "failed to list plugin defaults", "err", err, "user_id", req.UserId)
		return nil, status.Error(codes.Internal, "failed to list plugin defaults")
	}

	return &pbsvc.ListPluginDefaultsResponse{Defaults: defaults}, nil
}

func (s *Service) SetPluginDefaults(ctx context.Context, req *pbsvc.SetPluginDefaultsRequest) (*emptypb.Empty, error) {
	if req.UserId == "" || req.PluginId == "" || req.Defaults == nil {
		return nil, status.Error(codes.InvalidArgument, "user_id, plugin_id, and defaults are required")
	}

	err := s.store.SetPluginDefaults(ctx, req.UserId, req.PluginId, req.Defaults)
	if err != nil {
		s.logger.Error(ctx, "failed to set plugin defaults", "err", err, "user_id", req.UserId, "plugin_id", req.PluginId)
		return nil, status.Error(codes.Internal, "failed to set plugin defaults")
	}

	return &emptypb.Empty{}, nil
}

func (s *Service) DeletePluginDefaults(ctx context.Context, req *pbsvc.DeletePluginDefaultsRequest) (*emptypb.Empty, error) {
	if req.UserId == "" || req.PluginId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and plugin_id are required")
	}

	err := s.store.DeletePluginDefaults(ctx, req.UserId, req.PluginId)
	if err != nil {
		s.logger.Error(ctx, "failed to delete plugin defaults", "err", err, "user_id", req.UserId, "plugin_id", req.PluginId)
		return nil, status.Error(codes.Internal, "failed to delete plugin defaults")
	}

	return &emptypb.Empty{}, nil
}
