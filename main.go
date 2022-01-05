package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"hash/crc32"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

const shardCount = 200

func kycBatchHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok\n")
}

type KycResponse struct {
	UserId    string `json:"userId"`
	RequestId string `json:"requestId"`
}

type Kyc struct {
	UserId      string   `json:"userId"`
	TenantId 		string   `json:"tenantId"`
	FirstName   string   `json:"firstName"`
	LastName    string   `json:"lastName"`
	DateOfBirth string   `json:"dateOfBirth"` // ISO 8601
	RecordType  string   `json:"recordType"`
	KycStatus   string   `json:"kycStatus"`
	Address     *Address `json:"address"`
}

type Address struct {
	Address1            string `json:"address1"`
	Address2            string `json:"address2"`
	Address3            string `json:"address3"`
	Address4            string `json:"address4"`
	CityLocality        string `json:"cityLocality"`
	StateProvinceRegion string `json:"stateProvinceRegion"`
	PostalCode          string `json:"postalCode"`
	CountryCode         string `json:"countryCode"` // ISO 3166 Alpha-3 code
}

/**
 * KYC user ingestion.
 *
 * Path: /v1/ingestion/{tenantId}/kyc
 *
 * Post body:
 * {
 * "userId": "string",
 * "firstName": "string",
 * "lastName": "string",
 * "dateOfBirth": "2020-01-04", ISO 8601
 * "kycStatus": "string",
 * "recordType": "string",
 * "address": {
 *   "address1": " "string",
 *   "address2": " "string",
 *   "address3": " "string",
 *   "address4": " "string",
 *   "cityLocality": "string",
 *   "stateProvinceRegion": "string",
 *   "postalCode": "string"
 *   "countryCode": "USA" ISO 3166 Alpha-3 code
 * }
 * }
 *
 * Response status code: 200 ok
 * Response body:
 *
 * {
 *   "requestId": "UUID hex string",
 *   "userId": "string"
 * }
 */
func kycHandler(w http.ResponseWriter, r *http.Request) {

	vars := mux.Vars(r)

	requestId := newRequestId()

	tenantId := vars["tenantId"]

	// TODO: Move to a buffer reader with max length
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		fmt.Println(err)
		w.WriteHeader(http.StatusGatewayTimeout)
		return
	}

	var kyc Kyc
	if err := json.Unmarshal(body, &kyc); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Println(err)
		return
	}

	kyc.TenantId = tenantId

	if err := putKycItem(context.TODO(), ddb, tenantId, &kyc); err != nil {
		fmt.Printf("putKycItem failed - requestId: %s - err: %v\n", requestId, err)
		w.WriteHeader(http.StatusBadGateway)
	}

	response := &KycResponse{
		UserId:    kyc.UserId,
		RequestId: requestId,
	}

	out, err := json.Marshal(response)
	if err != nil {
		fmt.Printf("error marshaling JSON: %v\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(out); err != nil {
		fmt.Printf("error writting response JSON: %v\n", err)
	}
}

func kytHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok\n")
}

func kytBatchHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok\n")
}

var ddb *dynamodb.Client
var envName = "dev"
var kycTableName string

func main() {

	var wait time.Duration

	log.Println("starting")

	flag.DurationVar(&wait, "graceful-timeout", time.Minute*1, "the duration for which the server gracefully wait for existing connections to finish - e.g. 15s or 1m")
	flag.Parse()

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		panic(err)
	}

	ddb = dynamodb.NewFromConfig(cfg)

	router := mux.NewRouter()

	router.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "These aren't the droids you're looking for...\n")
	})

	router.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok\n")
	})

	// KYC
	router.HandleFunc("/v1/ingestion/{tenantId}/kyc", kycHandler).Methods("POST")

	// KYC (batch)
	router.HandleFunc("/v1/ingestion/{tenantId}/kycs", kycBatchHandler).Methods("POST")

	// KYT
	router.HandleFunc("/v1/ingestion/{tenantId}/kyt", kytHandler).Methods("POST")

	// KYT (batch)
	router.HandleFunc("/v1/ingestion/{tenantId}/kyts", kytBatchHandler).Methods("POST")

	if len(os.Getenv("ENV_NAME")) > 0 {
		envName = os.Getenv("ENV_NAME")
	}

	kycTableName = os.Getenv("KYC_TABLE")

	if len(kycTableName) == 0 {
		log.Fatal("KYC_TABLE env variable not set")
	}

	port := "8443"

	if len(os.Getenv("PORT")) > 0 {
		port = os.Getenv("PORT")
	}

	fmt.Println(fmt.Sprintf("starting listener on: %s", port))

	srv := &http.Server{
		Handler:      router,
		Addr:         fmt.Sprintf(":%s", port),
		WriteTimeout: 40 * time.Second,
		ReadTimeout:  40 * time.Second,
	}

	go func() {
		if err := srv.ListenAndServeTLS("server.crt", "server.key"); err != nil {
			log.Fatal("ListenAndServe: ", err)
		}
	}()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	<-c

	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()

	srv.Shutdown(ctx)

	// Optionally, you could run srv.Shutdown in a goroutine and block on
	// <-ctx.Done() if your application should wait for other services
	// to finalize based on context cancellation.
	log.Println("stopping")
	os.Exit(0)

}

func putKycItem(
	ctx context.Context,
	client *dynamodb.Client,
	tenantId string,
	kyc *Kyc,
) error {
	shardId := getShardId(tenantId, kyc.UserId)

	_, err := client.PutItem(context.TODO(), &dynamodb.PutItemInput{
		TableName: aws.String(kycTableName),
		Item: map[string]types.AttributeValue{
			"ShardId":    &types.AttributeValueMemberS{Value: shardId},
			"EntityId":   &types.AttributeValueMemberS{Value: fmt.Sprintf("A#%s#U", kyc.UserId)},
			"TenantId":   &types.AttributeValueMemberS{Value: kyc.TenantId},
			"UserId":     &types.AttributeValueMemberS{Value: kyc.UserId},
			"FirstName":  &types.AttributeValueMemberS{Value: kyc.FirstName},
			"LastName":   &types.AttributeValueMemberS{Value: kyc.LastName},
			"DOB":        &types.AttributeValueMemberS{Value: kyc.DateOfBirth},
			"RecordType": &types.AttributeValueMemberS{Value: kyc.RecordType},
			"KycStatus":  &types.AttributeValueMemberS{Value: kyc.KycStatus},
			"Address": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
				"Address1":            &types.AttributeValueMemberS{Value: kyc.Address.Address1},
				"Address2":            &types.AttributeValueMemberS{Value: kyc.Address.Address2},
				"Address3":            &types.AttributeValueMemberS{Value: kyc.Address.Address3},
				"Address4":            &types.AttributeValueMemberS{Value: kyc.Address.Address4},
				"CityLocality":        &types.AttributeValueMemberS{Value: kyc.Address.CityLocality},
				"StateProvinceRegion": &types.AttributeValueMemberS{Value: kyc.Address.StateProvinceRegion},
				"PostalCode":          &types.AttributeValueMemberS{Value: kyc.Address.PostalCode},
				"CountryCode":         &types.AttributeValueMemberS{Value: kyc.Address.CountryCode},
			}},
		},
	})

	return err
}

const IEEE = 0xedb88320

func getShardId(tenantId, userId string) string {

	crc32q := crc32.MakeTable(IEEE)

	v := crc32.Checksum([]byte(userId), crc32q)

	return fmt.Sprintf("%s-%d", tenantId, v % shardCount)
}

func newRequestId() string {
	return uuid.New().String()
}
