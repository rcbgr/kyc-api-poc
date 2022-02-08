package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	proto "github.com/golang/protobuf/proto"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	model "github.com/rnzsgh/kyc-api-poc/protob/model"
	"hash/crc32"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"
)

const shardCount = 200

var envVars = make(map[string]string)
var publicKeys = make(map[string]*rsa.PublicKey)
var publicKeyLock sync.Mutex

func kycBatchHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "ok\n")
}

type KycResponse struct {
	UserId    string `json:"userId"`
	RequestId string `json:"requestId"`
}

type Kyc struct {
	UserId      string `json:"userId"`
	TenantId    string `json:"tenantId"`
	FirstName   string `json:"firstName"`
	firstName   []byte
	LastName    string `json:"lastName"`
	lastName    []byte
	DateOfBirth string `json:"dateOfBirth"` // ISO 8601
	dateOfBirth []byte
	RecordType  string   `json:"recordType"`
	KycStatus   string   `json:"kycStatus"`
	Address     *Address `json:"address"`
	Id          *Id      `json:"id"`
}

type Id struct {
	Type        string `json:"type"`
	Value       string `json:"value"` // ISO 3166 Alpha-3 code
	value       []byte
	CountryCode string `json:"countryCode"` // ISO 3166 Alpha-3 code
}

type Address struct {
	Address1            string `json:"address1"`
	address1            []byte
	Address2            string `json:"address2"`
	address2            []byte
	Address3            string `json:"address3"`
	address3            []byte
	Address4            string `json:"address4"`
	address4            []byte
	CityLocality        string `json:"cityLocality"`
	StateProvinceRegion string `json:"stateProvinceRegion"`
	PostalCode          string `json:"postalCode"`
	CountryCode         string `json:"countryCode"` // ISO 3166 Alpha-3 code
}

type Order struct {
	UserId      string `json:"userId"`
	TenantId    string `json:"tenantId"`


}



func internalKyc(v *model.Kyc) (kyc *Kyc) {

	kyc = &Kyc{
		UserId:      v.UserId,
		FirstName:   v.FirstName,
		DateOfBirth: v.DateOfBirth,
		RecordType:  v.RecordType,
		KycStatus:   v.KycStatus,
		Address: &Address{
			Address1:            v.Address.Address1,
			CityLocality:        v.Address.CityLocality,
			StateProvinceRegion: v.Address.StateProvinceRegion,
			PostalCode:          v.Address.PostalCode,
			CountryCode:         v.Address.CountryCode,
		},
		Id: &Id{
			Type:        v.Id.Type,
			Value:       v.Id.Value,
			CountryCode: v.Id.CountryCode,
		},
	}

	if v.LastName != nil {
		kyc.LastName = *v.LastName
	}

	if v.Address.Address2 != nil {
		kyc.Address.Address2 = *v.Address.Address2
	}

	if v.Address.Address3 != nil {
		kyc.Address.Address3 = *v.Address.Address3
	}

	if v.Address.Address4 != nil {
		kyc.Address.Address4 = *v.Address.Address4
	}

	return
}

/**
 * Return the RSA_4096 key for the tenant or error if not found.
 */
func publicKey(tenantId string) (*rsa.PublicKey, error) {
	publicKeyLock.Lock()
	if key, ok := publicKeys[tenantId]; ok {
		return key, nil
	}
	publicKeyLock.Unlock()

	// Load key in KMS
	out, err := kmsc.GetPublicKey(context.TODO(), &kms.GetPublicKeyInput{
		KeyId: aws.String(fmt.Sprintf("alias/%s", tenantId)),
	})

	if err != nil {
		return nil, fmt.Errorf("Unable to load public key for TenantId: %s - err: %v", tenantId, err)
	}

	pub, err := x509.ParsePKIXPublicKey(out.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("Unable to prase public key for TenantId: %s - err: %v", tenantId, err)
	}

	key, _ := pub.(*rsa.PublicKey)

	publicKeyLock.Lock()
	publicKeys[tenantId] = key
	publicKeyLock.Unlock()

	return key, nil

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
 * "id": {
 *   "type": "string", e.g., SSN
 *   "value": "string",
 *   "countryCode": "USA" ISO 3166 Alpha-3 code
 * }
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
		fmt.Println(fmt.Sprintf("bad data passed - tenantId: %s - err: %v", tenantId, err))
		return
	}

	kyc.TenantId = tenantId

	if err := putKycItem(context.TODO(), ddb, &kyc); err != nil {
		fmt.Printf("putKycItem failed - requestId: %s - err: %v\n", requestId, err)
		w.WriteHeader(http.StatusBadGateway)
		return
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

func kycProtobufHandler(w http.ResponseWriter, r *http.Request) {

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

	kyc := &model.Kyc{}

	if err := proto.Unmarshal(body, kyc); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Println(fmt.Sprintf("bad data passed - tenantId: %s - err: %v", tenantId, err))
		return
	}

	item := internalKyc(kyc)

	item.TenantId = tenantId

	if err := putKycItem(context.TODO(), ddb, item); err != nil {
		fmt.Printf("putKycItem failed - requestId: %s - err: %v\n", requestId, err)
		w.WriteHeader(http.StatusBadGateway)
		return
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
var kmsc *kms.Client
var envName = "dev"
var kycTableName string

func main() {

	var wait time.Duration

	log.Println("starting")

	flag.DurationVar(&wait, "graceful-timeout", time.Minute*1, "the duration for which the server gracefully wait for existing connections to finish - e.g. 15s or 1m")
	flag.Parse()

	for _, e := range os.Environ() {
		pair := strings.Split(e, "=")
		envVars[pair[0]] = pair[1]
	}

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		panic(err)
	}

	ddb = dynamodb.NewFromConfig(cfg)

	kmsc = kms.NewFromConfig(cfg)

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

	// KYC V2
	router.HandleFunc("/v2/ingestion/{tenantId}/kyc", kycProtobufHandler).Methods("POST")

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

func encryptKyc(kyc *Kyc) error {

	rng := rand.Reader
	label := []byte("kyc")
	var err error

	key, err := publicKey(kyc.TenantId)
	if err != nil {
		return err
	}

	if kyc.firstName, err = rsa.EncryptOAEP(sha256.New(), rng, key, []byte(kyc.FirstName), label); err != nil {
		return fmt.Errorf("Unable to encrypt value - tenantId: %s - err: %v", kyc.TenantId, err)
	}

	if kyc.lastName, err = rsa.EncryptOAEP(sha256.New(), rng, key, []byte(kyc.LastName), label); err != nil {
		return fmt.Errorf("Unable to encrypt value - tenantId: %s - err: %v", kyc.TenantId, err)
	}

	if kyc.dateOfBirth, err = rsa.EncryptOAEP(sha256.New(), rng, key, []byte(kyc.DateOfBirth), label); err != nil {
		return fmt.Errorf("Unable to encrypt value - tenantId: %s - err: %v", kyc.TenantId, err)
	}

	if kyc.Id.value, err = rsa.EncryptOAEP(sha256.New(), rng, key, []byte(kyc.Id.Value), label); err != nil {
		return fmt.Errorf("Unable to encrypt value - tenantId: %s - err: %v", kyc.TenantId, err)
	}

	if kyc.Address.address1, err = rsa.EncryptOAEP(sha256.New(), rng, key, []byte(kyc.Address.Address1), label); err != nil {
		return fmt.Errorf("Unable to encrypt value - tenantId: %s - err: %v", kyc.TenantId, err)
	}

	if len(kyc.Address.Address2) > 0 {
		if kyc.Address.address2, err = rsa.EncryptOAEP(sha256.New(), rng, key, []byte(kyc.Address.Address2), label); err != nil {
			return fmt.Errorf("Unable to encrypt value - tenantId: %s - err: %v", kyc.TenantId, err)
		}
	} else {
		kyc.Address.address2 = make([]byte, 0)
	}

	if len(kyc.Address.Address3) > 0 {
		if kyc.Address.address3, err = rsa.EncryptOAEP(sha256.New(), rng, key, []byte(kyc.Address.Address3), label); err != nil {
			return fmt.Errorf("Unable to encrypt value - tenantId: %s - err: %v", kyc.TenantId, err)
		}
	} else {
		kyc.Address.address3 = make([]byte, 0)
	}

	if len(kyc.Address.Address4) > 0 {
		if kyc.Address.address4, err = rsa.EncryptOAEP(sha256.New(), rng, key, []byte(kyc.Address.Address4), label); err != nil {
			return fmt.Errorf("Unable to encrypt value - tenantId: %s - err: %v", kyc.TenantId, err)
		}
	} else {
		kyc.Address.address4 = make([]byte, 0)
	}

	return nil
}

func putKycItem(
	ctx context.Context,
	client *dynamodb.Client,
	kyc *Kyc,
) error {

	if err := encryptKyc(kyc); err != nil {
		return err
	}

	shardId := getShardId(kyc.TenantId, kyc.UserId)
	fmt.Println(shardId)

	_, err := client.PutItem(context.TODO(), &dynamodb.PutItemInput{
		TableName: aws.String(kycTableName),
		Item: map[string]types.AttributeValue{
			"ShardId":    &types.AttributeValueMemberS{Value: shardId},
			"EntityId":   &types.AttributeValueMemberS{Value: fmt.Sprintf("A#%s#U", kyc.UserId)},
			"TenantId":   &types.AttributeValueMemberS{Value: kyc.TenantId},
			"UserId":     &types.AttributeValueMemberS{Value: kyc.UserId},
			"FirstName":  &types.AttributeValueMemberB{Value: kyc.firstName},
			"LastName":   &types.AttributeValueMemberB{Value: kyc.lastName},
			"DOB":        &types.AttributeValueMemberB{Value: kyc.dateOfBirth},
			"RecordType": &types.AttributeValueMemberS{Value: kyc.RecordType},
			"KycStatus":  &types.AttributeValueMemberS{Value: kyc.KycStatus},
			"Id": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
				"Type":        &types.AttributeValueMemberS{Value: kyc.Id.Type},
				"Value":       &types.AttributeValueMemberB{Value: kyc.Id.value},
				"CountryCode": &types.AttributeValueMemberS{Value: kyc.Id.CountryCode},
			}},
			"Address": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
				"Address1":            &types.AttributeValueMemberB{Value: kyc.Address.address1},
				"Address2":            &types.AttributeValueMemberB{Value: kyc.Address.address2},
				"Address3":            &types.AttributeValueMemberB{Value: kyc.Address.address3},
				"Address4":            &types.AttributeValueMemberB{Value: kyc.Address.address4},
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

	return fmt.Sprintf("%s-%d", tenantId, (v % shardCount))
}

func newRequestId() string {
	return uuid.New().String()
}
