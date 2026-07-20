package config

import "os"

// HostedContractEnvironment is the sole environment boundary for the
// PostgreSQL/S3 repository contract exercised by integration tests.
func HostedContractEnvironment(bucket string) StorageConfig {
	return StorageConfig{
		PostgresURL: os.Getenv("FLIGHT_TEST_POSTGRES_URL"),
		S3Endpoint:  os.Getenv("FLIGHT_TEST_S3_ENDPOINT"),
		S3Bucket:    bucket,
		S3Region:    "us-east-1",
		S3AccessKey: os.Getenv("FLIGHT_TEST_S3_ACCESS_KEY"),
		S3SecretKey: os.Getenv("FLIGHT_TEST_S3_SECRET_KEY"),
	}
}

func RequireOCISecurityContract() bool {
	return os.Getenv("FLIGHT_REQUIRE_OCI_SECURITY") == "1"
}
