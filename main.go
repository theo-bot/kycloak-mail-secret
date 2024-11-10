package main

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"
	"os"
	"sync"

	"github.com/Nerzal/gocloak/v13"
	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	toolsWatch "k8s.io/client-go/tools/watch"
)

var (
	config, _    = clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
	clientset, _ = kubernetes.NewForConfig(config)
)

type kcConfig struct {
	url       string
	realm     string
	username  string
	password  string
	namespace string
}

type kcMail struct {
	username string
	password string
	host     string
}

// Get an environment variable or return a default value
func GetEnvironmentVar(envVar string, defaultValue string) string {
	val := os.Getenv(envVar)
	if val == "" {
		return defaultValue
	}
	return val
}

// The the SMTP values for a given keycloak service
func SetKeycloakSmtp(config *kcConfig, kcEmail *kcMail) error {
	client := gocloak.NewClient(config.url)
	ctx := context.Background()

	// Retrieve a token
	token, err := client.LoginAdmin(ctx, config.username, config.password, config.realm)
	if err != nil {
		log.Error("Failed to get token from keycloak. ", err.Error())
		return err
	}

	// Fetch the realm configuration
	kcRealm, err := client.GetRealm(ctx, token.AccessToken, config.realm)
	if err != nil {
		log.Error("Failed to get realm configuration. ", err.Error())
		return err
	}

	// Extract the SMPT configuration from the realm configuration
	kcSmtp := *kcRealm.SMTPServer
	log.Info(kcSmtp)

	// Update the smtp parameters
	kcSmtp["host"] = kcEmail.host
	kcSmtp["password"] = kcEmail.password
	kcSmtp["user"] = kcEmail.username

	// Update te realm configuration with the new smtp settings
	err = client.UpdateRealm(ctx, token.AccessToken, *kcRealm)
	if err != nil {
		log.Error("Failed to update REALM ", config.realm, ". ", err.Error())
		return err
	}

	return nil
}

func watchSecrets(config *kcConfig) error {

	// Get the current namespace
	namespace, err := clientset.CoreV1().Namespaces().Get(context.TODO(), "default", metav1.GetOptions{})
	if err != nil {
		log.Error("Failed to get current namespace. ", err.Error())
		return err
	}
	config.namespace = namespace.GetName()

	watchFunc := func(options metav1.ListOptions) (watch.Interface, error) {
		timeOut := int64(60)
		return clientset.CoreV1().Secrets(namespace.GetName()).Watch(context.Background(), metav1.ListOptions{TimeoutSeconds: &timeOut})
	}

	watcher, _ := toolsWatch.NewRetryWatcher("1", &cache.ListWatch{WatchFunc: watchFunc})

	// Let's act upon the kind of event type
	for event := range watcher.ResultChan() {
		item := event.Object.(*corev1.Secret)
		log.Info("Eventtype: ", event.Type)
		switch event.Type {
		case watch.Modified:
			modSecret(config, item.GetName())
		case watch.Bookmark:
		case watch.Error:
		case watch.Deleted:
		case watch.Added:
		}
	}

	return nil
}

func modSecret(config *kcConfig, name string) error {
	log.Info("Secret changed: ", name)
	secretName := GetEnvironmentVar("KEYCLOAK_SMTP_SECRET", "keycloak-smtp-secret")
	if name == secretName {

		// Get the secret from the current namespace
		secret, err := clientset.CoreV1().Secrets(config.namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
		if err != nil {
			log.Error("Failed to get secret. ", err.Error())
			return err
		}

		var smtpData *kcMail
		smtpData.username = string(secret.Data["username"])
		smtpData.password = string(secret.Data["password"])
		smtpData.host = string(secret.Data["hostname"])

		// Set the Smtp password in the keycloak pod
		SetKeycloakSmtp(config, smtpData)
	}

	return nil
}

func main() {
	var KcConfig *kcConfig
	KcConfig.url = GetEnvironmentVar("KC_URL", "keycloak.example.com")
	KcConfig.realm = GetEnvironmentVar("KC_REALM", "REV")
	KcConfig.username = GetEnvironmentVar("KC_ADMIN_USER", "admin")
	KcConfig.password = GetEnvironmentVar("KC_ADMIN_PASS", "admin")

	var wg sync.WaitGroup
	go watchSecrets(KcConfig)
	wg.Add(1)
	wg.Wait()
}
