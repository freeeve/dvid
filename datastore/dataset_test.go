package datastore

import (
	. "github.com/janelia-flyem/go/gocheck"
	_ "testing"

	"github.com/janelia-flyem/dvid/dvid"
)

func (s *DataSuite) TestNewDAG(c *C) {
	dag := NewVersionDAG()
	c.Assert(dag.NewVersionID, Equals, dvid.VersionLocalID(1))
	c.Assert(dag.Nodes, HasLen, 1)
	c.Assert(dag.VersionMap, HasLen, 1)
}

func (s *DataSuite) TestDatasetPersistence(c *C) {
	dir := c.MkDir()

	// Create a new datastore.
	err := Init(dir, true, dvid.Config{})
	c.Assert(err, IsNil)

	// Open the datastore
	service, err := Open(dir)
	c.Assert(err, IsNil)

	root, _, err := service.NewDataset()
	c.Assert(err, IsNil)

	c.Assert(service.Lock(root), IsNil)

	child1, err := service.NewVersion(root)
	c.Assert(err, IsNil)

	_, err = service.NewVersion(root)
	c.Assert(err, IsNil)

	c.Assert(service.Lock(child1), IsNil)

	_, err = service.NewVersion(child1)
	c.Assert(err, IsNil)

	oldJSON, err := service.DatasetsAllJSON()
	c.Assert(err, IsNil)

	service.Shutdown()

	// Open using different service
	service2, err := Open(dir)
	c.Assert(err, IsNil)

	newJSON, err := service2.DatasetsAllJSON()
	c.Assert(err, IsNil)

	c.Assert(newJSON, DeepEquals, oldJSON)
}

// Make sure each new dataset has a different local ID.
func (s *DataSuite) TestNewDatasetDifferent(c *C) {
	root1, datasetID1, err := s.service.NewDataset()
	c.Assert(err, IsNil)

	root2, datasetID2, err := s.service.NewDataset()
	c.Assert(err, IsNil)

	c.Assert(datasetID1, Not(Equals), datasetID2)
	c.Assert(root1, Not(Equals), root2)
}
