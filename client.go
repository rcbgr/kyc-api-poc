package client


import(
	"fmt"
	proto "github.com/golang/protobuf/proto"
	model "github.com/rnzsgh/kyc-api-poc/protob/model"
)

func main() {

	lastName := new(string)
	*lastName = "Last"

	kyc := &model.Kyc{
		UserId:      "sometestuserid",
		FirstName:   "First",
		LastName: lastName,
		DateOfBirth: "2020-01-10",
		RecordType: "KYC",
		KycStatus: "VERIFIED",
		Address     : &model.Address{
			Address1: "111 Here",
			CityLocality: "New York",
			StateProvinceRegion: "NY",
			PostalCode: "10004",
			CountryCode: "USA",
		},
		Id: &model.Id{
			Type: "SSN",
			Value: "111-11-1111",
			CountryCode: "USA",
		},
	}

	out, err := proto.Marshal(kyc)
	if err != nil {
		panic(fmt.Sprintf("Failed to marshal event: %v", err))
	}

}

