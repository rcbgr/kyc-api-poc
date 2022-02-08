

curl -v -X POST \
w-X POST \
	https://kyc.deftlabs.com/v1/ingestion/15e8ab60-6e3a11ec90d60242ac120003/kyc \
	-H 'Content-Type: application/json' \
	-d '{"userId":"testUserId04", "firstName":"Jimmy", "lastName": "McMillan", "dateOfBirth": "1946-12-01", "kycStatus": "verified", "recordType": "good", "address": { "address1": "111 Flatbush Ave", "cityLocality": "Brooklyn", "stateProvinceRegion": "NY", "postalCode": "11226", "countryCode": "USA"  }, "id": { "type": "SSN", "value": "111-11-1111", "countryCode": "USA" } }'

