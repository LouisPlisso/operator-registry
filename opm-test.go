package main

import (
	"context"
	"database/sql"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"time"

	"github.com/operator-framework/operator-registry/pkg/lib/bundle"
	"github.com/operator-framework/operator-registry/pkg/lib/indexer"
	"github.com/operator-framework/operator-registry/pkg/sqlite"
	"github.com/sirupsen/logrus"
)

var (
	DOCKER_USERNAME = os.Getenv("DOCKER_USERNAME")
	DOCKER_PASSWORD = os.Getenv("DOCKER_PASSWORD")

	packageName    = "prometheus"
	channels       = "preview"
	defaultChannel = "preview"

	bundlePath1 = "manifests/prometheus/0.14.0"
	bundlePath2 = "manifests/prometheus/0.15.0"
	bundlePath3 = "manifests/prometheus/0.22.2"

	bundleTag1 = StringWithCharset(6)
	bundleTag2 = StringWithCharset(6)
	bundleTag3 = StringWithCharset(6)
	indexTag   = StringWithCharset(6)

	bundleImage = "quay.io/olmtest/e2e-bundle"
	indexImage  = "quay.io/olmtest/e2e-index:" + indexTag
)

func StringWithCharset(length int) string {
	rand.Seed(time.Now().UnixNano())
	CHARSET := "abcdefghijklmnopqrstuvwxyz0123456789"
	randChars := make([]byte, length)
	for i := range randChars {
		randChars[i] = CHARSET[rand.Intn(len(CHARSET))]
	}
	return string(randChars)
}

func buildBundlesWith(containerTool string) error {
	err := bundle.BuildFunc(bundlePath1, bundleImage+":"+bundleTag1, containerTool, packageName, channels, defaultChannel, false)
	if err != nil {
		return err
	}
	err = bundle.BuildFunc(bundlePath2, bundleImage+":"+bundleTag2, containerTool, packageName, channels, defaultChannel, false)
	if err != nil {
		return err
	}
	err = bundle.BuildFunc(bundlePath3, bundleImage+":"+bundleTag3, containerTool, packageName, channels, defaultChannel, false)
	return err
}

func buildIndexWith(containerTool string) error {
	bundles := []string{
		bundleImage + ":" + bundleTag1,
		bundleImage + ":" + bundleTag2,
		bundleImage + ":" + bundleTag3,
	}
	logger := logrus.WithFields(logrus.Fields{"bundles": bundles})
	indexAdder := indexer.NewIndexAdder(containerTool, logger)

	request := indexer.AddToIndexRequest{
		Generate:          false,
		FromIndex:         "",
		BinarySourceImage: "",
		OutDockerfile:     "",
		Tag:               indexImage,
		Bundles:           bundles,
		Permissive:        false,
	}

	return indexAdder.AddToIndex(request)
}

func pushWith(containerTool, image string) error {
	dockerpush := exec.Command(containerTool, "push", image)
	return dockerpush.Run()
}

func pushBundles(containerTool string) error {
	err := pushWith(containerTool, bundleImage+":"+bundleTag1)
	if err != nil {
		return err
	}
	err = pushWith(containerTool, bundleImage+":"+bundleTag2)
	if err != nil {
		return err
	}
	err = pushWith(containerTool, bundleImage+":"+bundleTag3)
	return err
}

func exportWith(containerTool string) error {
	logger := logrus.WithFields(logrus.Fields{"package": packageName})
	indexExporter := indexer.NewIndexExporter(containerTool, logger)

	request := indexer.ExportFromIndexRequest{
		Index:         indexImage,
		Package:       packageName,
		DownloadPath:  "downloaded",
		ContainerTool: containerTool,
	}

	return indexExporter.ExportFromIndex(request)
}

func initialize() error {
	tmpDB, err := ioutil.TempFile("./", "index_tmp.db")
	if err != nil {
		return err
	}
	defer os.Remove(tmpDB.Name())

	db, err := sql.Open("sqlite3", tmpDB.Name())
	if err != nil {
		return err
	}
	defer db.Close()

	dbLoader, err := sqlite.NewSQLLiteLoader(db)
	if err != nil {
		return err
	}
	if err := dbLoader.Migrate(context.TODO()); err != nil {
		return err
	}

	loader := sqlite.NewSQLLoaderForDirectory(dbLoader, "downloaded")
	return loader.Populate()
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		log.Fatal("Must specify which container tool to use from [\"docker\", \"podman\"]")
	}
	if len(args) > 1 {
		log.Fatal("Too many command line arguments provided")
	}
	if args[0] != "docker" && args[0] != "podman" {
		log.Fatal("container tool argument must be one of [\"docker\", \"podman\"]")
	}

	containerTool := args[0]

	dockerlogin := exec.Command(containerTool, "login", "-u", DOCKER_USERNAME, "-p", DOCKER_PASSWORD, "quay.io")
	err := dockerlogin.Run()
	if err != nil {
		log.Fatal("Error logging into quay.io", err)
	}

	err = buildBundlesWith(containerTool)
	if err != nil {
		log.Fatal("Error building bundles", err)
	}

	err = pushBundles(containerTool)
	if err != nil {
		log.Fatal("Error pushing bundles", err)
	}

	err = buildIndexWith(containerTool)
	if err != nil {
		log.Fatal("Error building index", err)
	}

	err = pushWith(containerTool, indexImage)
	if err != nil {
		log.Fatal("Error pushing index", err)
	}

	err = exportWith(containerTool)
	if err != nil {
		log.Fatal("Error exporting from index", err)
	}

	err = initialize()
	if err != nil {
		log.Fatal("Error loading manifests from directory", err)
	}
}
