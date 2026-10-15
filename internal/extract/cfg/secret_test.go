package cfg

import "testing"

func TestIsSecret(t *testing.T) {
	for key, want := range map[string]bool{
		"db.password": true, "DB_PASSWORD": true, "smtp.passwd": true, "oauth-Secret": true,
		"access_token": true, "github.token": true, "apikey": true, "APIKEY": true, "apiKey": true,
		"API_KEY": true, "stripe.api_key.value": true, "aws.credentials": true, "tls.private-key": true,
		"tls.privateKey": true, "ssh.private_key.path": true, "jwt.signing-key": true,
		"s3.access.key": true, "kms.encryption_key": true, "app.secret.key": true,
		"spring.kafka.key-serializer": false, "configMapKeyRef.name": false, "quoted.key": false,
		"cache.key": false, "key": false, "primary.key.column": false, "keyboard": false,
		"monkey": false, "tokens": false, "AWS_ACCESS_KEY_ID": false, "tokenizer.model": false,
	} {
		if got := isSecret(key); got != want {
			t.Errorf("isSecret(%q) = %v, want %v", key, got, want)
		}
	}
}
