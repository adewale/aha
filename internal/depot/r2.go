package depot

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/adewale/aha/internal/model"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
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

func (d *R2) Init(ctx context.Context) error {
	_, err := d.Client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(d.Bucket)})
	if err != nil {
		if _, createErr := d.Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(d.Bucket)}); createErr != nil {
			return createErr
		}
	}
	existing, err := d.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String("depot.json")})
	if err == nil {
		var m marker
		decodeErr := json.NewDecoder(existing.Body).Decode(&m)
		_ = existing.Body.Close()
		if decodeErr != nil {
			return decodeErr
		}
		return validateMarker(m)
	}
	b, err := json.MarshalIndent(newMarker(), "", "  ")
	if err != nil {
		return err
	}
	_, err = d.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String("depot.json"), Body: strings.NewReader(string(append(b, '\n'))), ContentType: aws.String("application/json"), IfNoneMatch: aws.String("*")})
	return err
}

func (d *R2) PutBundle(ctx context.Context, bundlePath string) (BundleRef, bool, error) {
	ref, err := BundleRefFromPath(bundlePath)
	if err != nil {
		return BundleRef{}, false, err
	}
	return d.PutBundleKnown(ctx, bundlePath, ref)
}

func (d *R2) PutBundleKnown(ctx context.Context, bundlePath string, ref BundleRef) (BundleRef, bool, error) {
	if ref.Key == "" {
		ref.Key = BundleKey(ref.BundleSHA256)
	}
	if ref.Filename == "" {
		ref.Filename = filepath.Base(bundlePath)
	}
	if err := ValidateKnownBundleRef(ref); err != nil {
		return BundleRef{}, false, err
	}
	if ref.Bytes > 0 {
		st, err := os.Stat(bundlePath)
		if err != nil {
			return BundleRef{}, false, err
		}
		if st.Size() != ref.Bytes {
			return BundleRef{}, false, fmt.Errorf("known bundle size mismatch: ref=%d actual=%d", ref.Bytes, st.Size())
		}
	}
	created := false
	_, headErr := d.Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String(ref.Key)})
	if headErr != nil {
		f, err := os.Open(bundlePath)
		if err != nil {
			return BundleRef{}, false, err
		}
		defer f.Close()
		_, err = d.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String(ref.Key), Body: f, ContentType: aws.String("application/zstd"), IfNoneMatch: aws.String("*")})
		if err != nil {
			return BundleRef{}, false, err
		}
		created = true
	}
	if err := d.appendCatalog(ctx, ref); err != nil {
		return BundleRef{}, false, err
	}
	return ref, created, nil
}

func (d *R2) appendCatalog(ctx context.Context, ref BundleRef) error {
	key := CatalogKey(ref.MachineID)
	for attempt := 0; attempt < 5; attempt++ {
		shard := CatalogShard{Schema: CatalogSchema, MachineID: ref.MachineID}
		var etag *string
		obj, err := d.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String(key)})
		if err == nil {
			etag = obj.ETag
			_ = json.NewDecoder(obj.Body).Decode(&shard)
			_ = obj.Body.Close()
		}
		if shard.Schema == "" {
			shard.Schema = CatalogSchema
		}
		if shard.MachineID == "" {
			shard.MachineID = ref.MachineID
		}
		shard.Bundles = mergeBundleRef(shard.Bundles, ref)
		sortRefs(shard.Bundles)
		b, err := json.MarshalIndent(shard, "", "  ")
		if err != nil {
			return err
		}
		input := &s3.PutObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String(key), Body: strings.NewReader(string(append(b, '\n'))), ContentType: aws.String("application/json")}
		if etag == nil {
			input.IfNoneMatch = aws.String("*")
		} else {
			input.IfMatch = etag
		}
		if _, err := d.Client.PutObject(ctx, input); err == nil {
			return nil
		}
	}
	return fmt.Errorf("catalog update conflict for %s", key)
}

func (d *R2) List(ctx context.Context) ([]BundleRef, error) {
	var out []BundleRef
	prefix := "catalog/v1/"
	p := s3.NewListObjectsV2Paginator(d.Client, &s3.ListObjectsV2Input{Bucket: aws.String(d.Bucket), Prefix: aws.String(prefix)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, obj := range page.Contents {
			got, err := d.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(d.Bucket), Key: obj.Key})
			if err != nil {
				return nil, err
			}
			var shard CatalogShard
			err = json.NewDecoder(got.Body).Decode(&shard)
			_ = got.Body.Close()
			if err != nil {
				return nil, err
			}
			out = append(out, shard.Bundles...)
		}
	}
	sortRefs(out)
	return out, nil
}

func (d *R2) Fetch(ctx context.Context, ref BundleRef, dst string) error {
	key := ref.Key
	if key == "" {
		key = BundleKey(ref.BundleSHA256)
	}
	if err := ValidateBundleKey(key); err != nil {
		return err
	}
	obj, err := d.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String(key)})
	if err != nil {
		return err
	}
	defer obj.Body.Close()
	return writeReaderToFile(dst, obj.Body)
}

func (d *R2) VerifyWithOptions(ctx context.Context, opts VerifyOptions) (VerifyReport, error) {
	if opts.Deep || opts.Repair {
		return d.Verify(ctx, opts.Repair)
	}
	return d.verifyQuick(ctx)
}

func (d *R2) verifyQuick(ctx context.Context) (VerifyReport, error) {
	report := VerifyReport{Deep: false}
	if obj, err := d.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String("depot.json")}); err == nil {
		var m marker
		decodeErr := json.NewDecoder(obj.Body).Decode(&m)
		_ = obj.Body.Close()
		if decodeErr != nil {
			report.Problems = append(report.Problems, "invalid depot marker: "+decodeErr.Error())
		} else if err := validateMarker(m); err != nil {
			report.Problems = append(report.Problems, "invalid depot marker: "+err.Error())
		}
	} else {
		report.Problems = append(report.Problems, "missing depot marker")
	}
	catalogRefs, err := d.List(ctx)
	if err != nil {
		return report, err
	}
	report.Catalogs = len(catalogRefs)
	seen := map[string]bool{}
	for _, ref := range catalogRefs {
		if ref.BundleSHA256 == "" {
			report.Problems = append(report.Problems, "catalog reference missing bundle sha")
			continue
		}
		if seen[ref.BundleSHA256] {
			continue
		}
		seen[ref.BundleSHA256] = true
		key := ref.Key
		if key == "" {
			key = BundleKey(ref.BundleSHA256)
		}
		if key != BundleKey(ref.BundleSHA256) {
			report.Problems = append(report.Problems, "catalog key mismatch "+ref.BundleSHA256)
			continue
		}
		if err := ValidateBundleKey(key); err != nil {
			report.Problems = append(report.Problems, err.Error())
			continue
		}
		if _, err := d.Client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String(key)}); err != nil {
			report.Problems = append(report.Problems, "catalog references missing bundle "+ref.BundleSHA256)
			continue
		}
		report.Bundles++
	}
	return report, nil
}

func (d *R2) Verify(ctx context.Context, repair bool) (VerifyReport, error) {
	report := VerifyReport{Deep: true}
	if obj, err := d.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String("depot.json")}); err == nil {
		var m marker
		decodeErr := json.NewDecoder(obj.Body).Decode(&m)
		_ = obj.Body.Close()
		if decodeErr != nil {
			report.Problems = append(report.Problems, "invalid depot marker: "+decodeErr.Error())
		} else if err := validateMarker(m); err != nil {
			report.Problems = append(report.Problems, "invalid depot marker: "+err.Error())
		}
	} else {
		report.Problems = append(report.Problems, "missing depot marker")
		if repair {
			b, err := json.MarshalIndent(newMarker(), "", "  ")
			if err != nil {
				return report, err
			}
			_, err = d.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String("depot.json"), Body: strings.NewReader(string(append(b, '\n'))), ContentType: aws.String("application/json"), IfNoneMatch: aws.String("*")})
			if err != nil {
				return report, err
			}
		}
	}
	catalogRefs, err := d.List(ctx)
	if err != nil {
		if !repair {
			return report, err
		}
		report.Problems = append(report.Problems, err.Error())
	}
	report.Catalogs = len(catalogRefs)
	catalogBySHA := map[string]bool{}
	for _, ref := range catalogRefs {
		catalogBySHA[ref.BundleSHA256] = true
	}
	refsBySHA := map[string]BundleRef{}
	var bundleRefs []BundleRef
	p := s3.NewListObjectsV2Paginator(d.Client, &s3.ListObjectsV2Input{Bucket: aws.String(d.Bucket), Prefix: aws.String("bundles/v1/")})
	tmpDir, tmpErr := os.MkdirTemp("", "aha-r2-verify-*")
	if tmpErr != nil {
		return report, tmpErr
	}
	defer os.RemoveAll(tmpDir)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return report, err
		}
		for _, obj := range page.Contents {
			if obj.Key == nil || !strings.HasSuffix(*obj.Key, ".tar.zst") {
				continue
			}
			sha := strings.TrimSuffix(strings.TrimPrefix(*obj.Key, "bundles/v1/"), ".tar.zst")
			report.Bundles++
			if err := ValidateBundleKey(*obj.Key); err != nil {
				report.Problems = append(report.Problems, err.Error())
				continue
			}
			ref := BundleRef{BundleSHA256: sha, Key: *obj.Key}
			path := filepath.Join(tmpDir, sha+".tar.zst")
			if err := d.Fetch(ctx, ref, path); err != nil {
				report.Problems = append(report.Problems, fmt.Sprintf("fetch %s: %v", sha, err))
				continue
			}
			full, err := BundleRefFromPath(path)
			if err != nil {
				report.Problems = append(report.Problems, fmt.Sprintf("read %s: %v", sha, err))
				continue
			}
			if full.BundleSHA256 != sha {
				report.Problems = append(report.Problems, fmt.Sprintf("bundle key/content sha mismatch %s", *obj.Key))
				continue
			}
			refsBySHA[sha] = full
			if !catalogBySHA[sha] {
				report.Problems = append(report.Problems, "catalog missing bundle "+sha)
			}
			if repair {
				bundleRefs = append(bundleRefs, full)
			}
		}
	}
	for _, ref := range catalogRefs {
		if _, ok := refsBySHA[ref.BundleSHA256]; !ok {
			report.Problems = append(report.Problems, "catalog references missing bundle "+ref.BundleSHA256)
		}
	}
	if repair {
		if err := d.deleteCatalogShards(ctx); err != nil {
			return report, err
		}
		byMachine := map[string][]BundleRef{}
		for _, ref := range bundleRefs {
			byMachine[ref.MachineID] = mergeBundleRef(byMachine[ref.MachineID], ref)
		}
		for machine, refs := range byMachine {
			sortRefs(refs)
			shard := CatalogShard{Schema: CatalogSchema, MachineID: machine, Bundles: refs}
			b, err := json.MarshalIndent(shard, "", "  ")
			if err != nil {
				return report, err
			}
			_, err = d.Client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(d.Bucket), Key: aws.String(CatalogKey(machine)), Body: strings.NewReader(string(append(b, '\n'))), ContentType: aws.String("application/json")})
			if err != nil {
				return report, err
			}
		}
		report.Repaired = true
	}
	return report, nil
}

func (d *R2) deleteCatalogShards(ctx context.Context) error {
	p := s3.NewListObjectsV2Paginator(d.Client, &s3.ListObjectsV2Input{Bucket: aws.String(d.Bucket), Prefix: aws.String("catalog/v1/")})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, obj := range page.Contents {
			if obj.Key == nil {
				continue
			}
			if _, err := d.Client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(d.Bucket), Key: obj.Key}); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeReaderToFile(dst string, r interface{ Read([]byte) (int, error) }) error {
	return copyFromReader(dst, r)
}

func copyFromReader(dst string, r interface{ Read([]byte) (int, error) }) error {
	if err := os.MkdirAll(dirOf(dst), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := f.ReadFrom(r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return copyErr
	}
	return closeErr
}

func dirOf(path string) string {
	if i := strings.LastIndex(path, string(os.PathSeparator)); i >= 0 {
		return path[:i]
	}
	return "."
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
