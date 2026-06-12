package depot

import (
	"errors"
	"fmt"
	"os"

	"github.com/adewale/aha/internal/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
)

type R2Config struct {
	AccountID       string
	Endpoint        string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

type R2 struct {
	Bucket string
	Client *s3.Client
	Config R2Config
}

func ResolveR2Config(cfg model.R2DepotConfig) (R2Config, error) {
	out := R2Config{
		AccountID:       firstEnv("AHA_R2_ACCOUNT_ID", "R2_ACCOUNT_ID", cfg.AccountID),
		Endpoint:        firstEnv("AHA_R2_ENDPOINT", "R2_ENDPOINT", cfg.Endpoint),
		Region:          firstEnv("AHA_R2_REGION", "R2_REGION", cfg.Region),
		AccessKeyID:     firstEnv("AHA_R2_ACCESS_KEY_ID", "R2_ACCESS_KEY_ID", ""),
		SecretAccessKey: firstEnv("AHA_R2_SECRET_ACCESS_KEY", "R2_SECRET_ACCESS_KEY", ""),
	}
	if out.Region == "" {
		out.Region = "auto"
	}
	if out.Endpoint == "" {
		if out.AccountID == "" {
			return out, fmt.Errorf("R2 account id required (AHA_R2_ACCOUNT_ID or R2_ACCOUNT_ID)")
		}
		out.Endpoint = fmt.Sprintf("https://%s.r2.cloudflarestorage.com", out.AccountID)
	}
	if out.AccessKeyID == "" || out.SecretAccessKey == "" {
		return out, fmt.Errorf("R2 credentials required (AHA_R2_ACCESS_KEY_ID/R2_ACCESS_KEY_ID and AHA_R2_SECRET_ACCESS_KEY/R2_SECRET_ACCESS_KEY)")
	}
	return out, nil
}

func NewR2(bucket string, cfg R2Config) *R2 {
	if bucket == "" {
		bucket = DefaultR2Bucket
	}
	client := s3.New(s3.Options{Region: cfg.Region, BaseEndpoint: aws.String(cfg.Endpoint), Credentials: credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, "")})
	return &R2{Bucket: bucket, Client: client, Config: cfg}
}

func (d *R2) Address() Address { return Address{Type: "r2", Location: d.Bucket} }

func isS3NotFound(err error) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.ErrorCode() {
	case "NoSuchKey", "NotFound", "404":
		return true
	default:
		return false
	}
}

func firstEnv(a, b, fallback string) string {
	if v := os.Getenv(a); v != "" {
		return v
	}
	if v := os.Getenv(b); v != "" {
		return v
	}
	return fallback
}
