package dockerscope

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"time"
	"github.com/alexflint/go-filemutex"
	"strconv"
)

const (
	layerConfigFile  = "json"
	imageConfigFile  = "repositories"
	workingDirectory = "/tmp"
)

type Layer struct {
	Id      string
	Created time.Time
}

type Repository struct {
}

type ByCreated []*Layer

func (a ByCreated) Len() int           { return len(a) }
func (a ByCreated) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByCreated) Less(i, j int) bool { return a[i].Created.After(a[j].Created) }

type Image struct {
	PathToSource      string
	Layers            []*Layer
	pathToWorkingCopy string
}

func randomFilename() string {
	return strconv.Itoa(rand.Intn(100000000))
}

// NewImage initalized the image located at pathToImage by untaring it
func NewImage(pathToImage string) (*Image, error) {

	if _, err := os.Stat(pathToImage); os.IsNotExist(err) {
		return nil, fmt.Errorf("No image found at path %s", pathToImage)
	}

	if filepath.Ext(pathToImage) == ".gz" {
		return nil, fmt.Errorf("Image must be an uncompressed tar file %s", pathToImage)
	}

	tmpDirPath := workingDirectory + string(filepath.Separator) + randomFilename()
	os.Mkdir(tmpDirPath, 0777)

	return &Image{PathToSource: pathToImage, pathToWorkingCopy: tmpDirPath}, nil

}

//Close removes any temporary data and updates the original image
func (i *Image) Close() {
	os.RemoveAll(i.pathToWorkingCopy)
}

//SetName changes the name of the image
func (i *Image) SetName(newName string) error {

	m, err := filemutex.New(i.PathToSource)
	if err != nil {
		return fmt.Errorf("Error renaming image: Setting mutex failed) %s", i.PathToSource)
	}
	m.Lock()
	defer m.Unlock()

	// untar image
	if err := untar(i.PathToSource, i.pathToWorkingCopy); err != nil {
		return fmt.Errorf("Error creating image: Untar failed) %s", i.pathToWorkingCopy)
	}

	repoPath := i.pathToWorkingCopy + string(filepath.Separator) + imageConfigFile

	data := []byte{}

	if _, err := os.Stat(repoPath); os.IsNotExist(err) {

		// if no repo file exists, create new repo file
		fmt.Println("not existing")

		l, err := i.latestLayer()

		if err != nil {
			return err
		}

		const latestLayerKey = "latest"

		newRepo := make(map[string]map[string]string)

		newRepo[newName] = make(map[string]string)

		newRepo[newName][latestLayerKey] = l.Id

		data, err = json.Marshal(newRepo)

		if err != nil {
			return fmt.Errorf("Error renaming image: Json failed %s", i.pathToWorkingCopy)
		}

	} else {

		fmt.Println("existing")

		// modify existing repo file

		d, err := ioutil.ReadFile(i.pathToWorkingCopy + string(filepath.Separator) + imageConfigFile)
		if err != nil {
			return fmt.Errorf("Failed to read docker config for image %s", i.pathToWorkingCopy)
		}

		//replace name in repository file with new image name
		var repo map[string]interface{}

		err = json.Unmarshal(d, &repo)
		if err != nil || len(repo) > 1 {
			return fmt.Errorf("Unexpected data schema for repository json in image  %s", i.pathToWorkingCopy)
		}

		var newImageName = map[string]interface{}{}

		for _, v := range repo {
			newImageName[newName] = v
		}

		data, err = json.Marshal(newImageName)

		if err != nil {
			return fmt.Errorf("Error creating retagged application image  %s", i.pathToWorkingCopy)
		}

	}

	// write new repo file

	if err = ioutil.WriteFile(repoPath, data, 0644); err != nil {
		return fmt.Errorf("Error renaming image: Repository write failed) %s", i.pathToWorkingCopy)
	}

	// put everything together again
	if err = tarit(i.pathToWorkingCopy, i.PathToSource); err != nil {
		return fmt.Errorf("Error creating image: Tar failed) %s", i.pathToWorkingCopy)
	}

	return nil

}

//latestLayer return the layer that was added last to the image
func (i *Image) latestLayer() (*Layer, error) {

	if len(i.Layers) == 0 {
		return nil, fmt.Errorf("Image has no layers")
	}

	sort.Sort(ByCreated(i.Layers))

	return i.Layers[0], nil

}

func (i *Image) readLayers() error {

	l := make([]*Layer, 0)

	err := filepath.Walk(i.pathToWorkingCopy, func(path string, info os.FileInfo, err error) error {

		dir, file := filepath.Split(path)

		if file == layerConfigFile {

			layerId := filepath.Base(dir)

			data, err := ioutil.ReadFile(path)

			if err != nil {
				return fmt.Errorf("Unexpected data schema in image %s", path)
			}

			var layerConfig map[string]interface{}

			err = json.Unmarshal(data, &layerConfig)

			if err != nil {
				return fmt.Errorf("Unexpected data schema in image %s", path)
			}

			r, e := layerConfig["created"].(string)

			if !e {
				return fmt.Errorf("Unexpected schema for `created` field in image layer %s", path)
			}

			layerCreationTime, err := time.Parse(time.RFC3339, r)

			if err != nil {
				return fmt.Errorf("Unexpected time schema in image layer %s", path)
			}

			l = append(l, &Layer{Id: layerId, Created: layerCreationTime})

		}

		return nil

	})

	if err != nil {
		return err
	}

	i.Layers = l

	return nil

}
