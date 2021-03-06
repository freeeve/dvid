/*
	Package multichan16 tailors the voxels data type for 16-bit fluorescent images with multiple
	channels that can be read from V3D Raw format.  Note that this data type has multiple
	channels but segregates its channel data in (c, z, y, x) fashion rather than interleave
	it within a block of data in (z, y, x, c) fashion.  There is not much advantage at
	using interleaving; most forms of RGB compression fails to preserve the
	independence of the channels.  Segregating the channel data lets us use straightforward
	compression on channel slices.

	Specific channels of multichan16 data are addressed by adding a numerical suffix to the
	data name.  For example, if we have "mydata" multichan16 data, we reference channel 1
	as "mydata1" and channel 2 as "mydata2".  Up to the first 3 channels are composited
	into a RGBA volume that is addressible using "mydata" or "mydata0".
*/
package multichan16

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/janelia-flyem/dvid/datastore"
	"github.com/janelia-flyem/dvid/datatype/voxels"
	"github.com/janelia-flyem/dvid/dvid"
	"github.com/janelia-flyem/dvid/server"
)

const (
	Version = "0.1"
	RepoUrl = "github.com/janelia-flyem/dvid/datatype/multichan16"
)

const HelpMessage = `
API for datatypes derived from multichan16 (github.com/janelia-flyem/dvid/datatype/multichan16)
===============================================================================================

Command-line:

$ dvid node <UUID> <data name> load <V3D raw filename>

    Adds multichannel data to a version node when the server can see the local files ("local")
    or when the server must be sent the files via rpc ("remote").

    Example: 

    $ dvid node 3f8c mydata load local mydata.v3draw

    Arguments:

    UUID          Hexidecimal string with enough characters to uniquely identify a version node.
    data name     Name of data to add.
    filename      Filename of a V3D Raw format file.
	
    ------------------

HTTP API (Level 2 REST):

GET  <api URL>/node/<UUID>/<data name>/help

	Returns data-specific help message.


GET  <api URL>/node/<UUID>/<data name>/info
POST <api URL>/node/<UUID>/<data name>/info

    Retrieves or puts data properties.

    Example: 

    GET <api URL>/node/3f8c/multichan16/info

    Returns JSON with configuration settings.

    Arguments:

    UUID          Hexidecimal string with enough characters to uniquely identify a version node.
    data name     Name of multichan16 data.


GET  <api URL>/node/<UUID>/<data name>/<dims>/<size>/<offset>[/<format>]
POST <api URL>/node/<UUID>/<data name>/<dims>/<size>/<offset>[/<format>]

    Retrieves or puts orthogonal plane image data to named multichannel 16-bit data.

    Example: 

    GET <api URL>/node/3f8c/mydata2/xy/200,200/0,0,100/jpg:80  (channel 2 of mydata)

    Arguments:

    UUID          Hexidecimal string with enough characters to uniquely identify a version node.
    data name     Name of data.  Optionally add a numerical suffix for the channel number.
    dims          The axes of data extraction in form "i_j_k,..."  Example: "0_2" can be XZ.
                    Slice strings ("xy", "xz", or "yz") are also accepted.
    size          Size in pixels in the format "dx_dy".
    offset        3d coordinate in the format "x_y_z".  Gives coordinate of top upper left voxel.
    format        Valid formats depend on the dimensionality of the request and formats
                    available in server implementation.
                  2D: "png", "jpg" (default: "png")
                    jpg allows lossy quality setting, e.g., "jpg:80"

`

// DefaultBlockMax specifies the default size for each block of this data type.
var (
	DefaultBlockSize int32 = 32

	typeService datastore.TypeService

	compositeValues = dvid.DataValues{
		{
			T:     dvid.T_uint8,
			Label: "red",
		},
		{
			T:     dvid.T_uint8,
			Label: "green",
		},
		{
			T:     dvid.T_uint8,
			Label: "blue",
		},
		{
			T:     dvid.T_uint8,
			Label: "alpha",
		},
	}
)

func init() {
	interpolable := true
	dtype := &Datatype{voxels.NewDatatype(nil, interpolable)}
	dtype.DatatypeID = datastore.MakeDatatypeID("multichan16", RepoUrl, Version)

	// See doc for package on why channels are segregated instead of interleaved.
	// Data types must be registered with the datastore to be used.
	typeService = dtype
	datastore.RegisterDatatype(dtype)

	// Need to register types that will be used to fulfill interfaces.
	gob.Register(&Datatype{})
	gob.Register(&Data{})
}

// -------  ExtHandler interface implementation -------------

// Channel is an image volume that fulfills the voxels.ExtHandler interface.
type Channel struct {
	*voxels.Voxels

	// Channel 0 is the composite RGBA channel and all others are 16-bit.
	channelNum int32
}

func (c *Channel) String() string {
	return fmt.Sprintf("Channel %d of size %s @ offset %s", c.channelNum, c.Size(), c.StartPoint())
}

func (c *Channel) Interpolable() bool {
	return true
}

// Index returns a channel-specific Index
func (c *Channel) Index(p dvid.ChunkPoint) dvid.Index {
	return dvid.IndexCZYX{c.channelNum, dvid.IndexZYX(p.(dvid.ChunkPoint3d))}
}

// IndexIterator returns an iterator that can move across the voxel geometry,
// generating indices or index spans.
func (c *Channel) IndexIterator(chunkSize dvid.Point) (dvid.IndexIterator, error) {
	// Setup traversal
	begVoxel, ok := c.StartPoint().(dvid.Chunkable)
	if !ok {
		return nil, fmt.Errorf("ExtHandler StartPoint() cannot handle Chunkable points.")
	}
	endVoxel, ok := c.EndPoint().(dvid.Chunkable)
	if !ok {
		return nil, fmt.Errorf("ExtHandler EndPoint() cannot handle Chunkable points.")
	}

	blockSize := chunkSize.(dvid.Point3d)
	begBlock := begVoxel.Chunk(blockSize).(dvid.ChunkPoint3d)
	endBlock := endVoxel.Chunk(blockSize).(dvid.ChunkPoint3d)

	return dvid.NewIndexCZYXIterator(c.channelNum, begBlock, endBlock), nil
}

// Datatype just uses voxels data type by composition.
type Datatype struct {
	*voxels.Datatype
}

// --- TypeService interface ---

// NewData returns a pointer to a new multichan16 with default values.
func (dtype *Datatype) NewDataService(id *datastore.DataID, config dvid.Config) (
	datastore.DataService, error) {

	voxelservice, err := dtype.Datatype.NewDataService(id, config)
	if err != nil {
		return nil, err
	}
	basedata := voxelservice.(*voxels.Data)
	basedata.Properties.Values = nil
	service := &Data{
		Data: *basedata,
	}
	return service, nil
}

func (dtype *Datatype) Help() string {
	return HelpMessage
}

// Data of multichan16 type embeds voxels and extends it with channels.
type Data struct {
	voxels.Data

	// Number of channels for this data.  The names are referenced by
	// adding a number onto the data name, e.g., mydata1, mydata2, etc.
	NumChannels int
}

// JSONString returns the JSON for this Data's configuration
func (d *Data) JSONString() (string, error) {
	m, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return string(m), nil
}

// --- DataService interface ---

// Do acts as a switchboard for RPC commands.
func (d *Data) DoRPC(request datastore.Request, reply *datastore.Response) error {
	if request.TypeCommand() != "load" {
		return d.UnknownCommand(request)
	}
	if len(request.Command) < 5 {
		return fmt.Errorf("Poorly formatted load command.  See command-line help.")
	}
	return d.LoadLocal(request, reply)
}

// DoHTTP handles all incoming HTTP requests for this dataset.
func (d *Data) DoHTTP(uuid dvid.UUID, w http.ResponseWriter, r *http.Request) error {
	startTime := time.Now()

	// Allow cross-origin resource sharing.
	w.Header().Add("Access-Control-Allow-Origin", "*")

	// Get the action (GET, POST)
	action := strings.ToLower(r.Method)
	var op voxels.OpType
	switch action {
	case "get":
		op = voxels.GetOp
	case "post":
		op = voxels.PutOp
	default:
		return fmt.Errorf("Can only handle GET or POST HTTP verbs")
	}

	// Break URL request into arguments
	url := r.URL.Path[len(server.WebAPIPath):]
	parts := strings.Split(url, "/")
	if len(parts) < 4 {
		err := fmt.Errorf("Incomplete API request")
		server.BadRequest(w, r, err.Error())
		return err
	}

	// Process help and info.
	switch parts[3] {
	case "help":
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintln(w, d.Help())
		return nil
	case "info":
		jsonStr, err := d.JSONString()
		if err != nil {
			server.BadRequest(w, r, err.Error())
			return err
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, jsonStr)
		return nil
	default:
	}

	// Get the data name and parse out the channel number or see if composite is required.
	var channelNum int32
	channumStr := strings.TrimPrefix(parts[2], string(d.Name))
	if len(channumStr) == 0 {
		channelNum = 0
	} else {
		n, err := strconv.ParseInt(channumStr, 10, 32)
		if err != nil {
			return fmt.Errorf("Error parsing channel number from data name '%s': %s",
				parts[2], err.Error())
		}
		if int(n) > d.NumChannels {
			minChannelName := fmt.Sprintf("%s1", d.DataName())
			maxChannelName := fmt.Sprintf("%s%d", d.DataName(), d.NumChannels)
			return fmt.Errorf("Data only has %d channels.  Use names '%s' -> '%s'", d.NumChannels,
				minChannelName, maxChannelName)
		}
		channelNum = int32(n)
	}

	// Get the data shape.
	shapeStr := dvid.DataShapeString(parts[3])
	dataShape, err := shapeStr.DataShape()
	if err != nil {
		return fmt.Errorf("Bad data shape given '%s'", shapeStr)
	}

	switch dataShape.ShapeDimensions() {
	case 2:
		sizeStr, offsetStr := parts[4], parts[5]
		slice, err := dvid.NewSliceFromStrings(shapeStr, offsetStr, sizeStr, "_")
		if err != nil {
			return err
		}
		if op == voxels.PutOp {
			return fmt.Errorf("DVID does not yet support POST of slices into multichannel data")
		} else {
			if d.NumChannels == 0 || d.Data.Values() == nil {
				return fmt.Errorf("Cannot retrieve absent data '%d'.  Please load data.", d.DataName())
			}
			values := d.Data.Values()
			if len(values) <= int(channelNum) {
				return fmt.Errorf("Must choose channel from 0 to %d", len(values))
			}
			stride := slice.Size().Value(0) * values.BytesPerElement()
			dataValues := dvid.DataValues{values[channelNum]}
			data := make([]uint8, int(slice.NumVoxels()))
			v := voxels.NewVoxels(slice, dataValues, data, stride, d.ByteOrder)
			channel := &Channel{
				Voxels:     v,
				channelNum: channelNum,
			}
			img, err := voxels.GetImage(uuid, d, channel)
			var formatStr string
			if len(parts) >= 7 {
				formatStr = parts[6]
			}
			//dvid.ElapsedTime(dvid.Normal, startTime, "%s %s upto image formatting", op, slice)
			err = dvid.WriteImageHttp(w, img.Get(), formatStr)
			if err != nil {
				server.BadRequest(w, r, err.Error())
				return err
			}
		}
	case 3:
		sizeStr, offsetStr := parts[4], parts[5]
		_, err := dvid.NewSubvolumeFromStrings(offsetStr, sizeStr, "_")
		if err != nil {
			server.BadRequest(w, r, err.Error())
			return err
		}
		if op == voxels.GetOp {
			err := fmt.Errorf("DVID does not yet support GET of volume data")
			server.BadRequest(w, r, err.Error())
			return err
		} else {
			err := fmt.Errorf("DVID does not yet support POST of volume data")
			server.BadRequest(w, r, err.Error())
			return err
		}
	default:
		err := fmt.Errorf("DVID does not yet support nD volumes")
		server.BadRequest(w, r, err.Error())
		return err
	}

	dvid.ElapsedTime(dvid.Debug, startTime, "HTTP %s: %s", r.Method, dataShape)
	return nil
}

// LoadLocal adds image data to a version node.  See HelpMessage for example of
// command-line use of "load local".
func (d *Data) LoadLocal(request datastore.Request, reply *datastore.Response) error {
	startTime := time.Now()

	// Get the running datastore service from this DVID instance.
	service := server.DatastoreService()

	// Parse the request
	var uuidStr, dataName, cmdStr, sourceStr, filename string
	_ = request.CommandArgs(1, &uuidStr, &dataName, &cmdStr, &sourceStr, &filename)

	// Get the uuid from a uniquely identifiable string
	uuid, _, _, err := service.NodeIDFromString(uuidStr)
	if err != nil {
		return fmt.Errorf("Could not find node with UUID %s: %s", uuidStr, err.Error())
	}

	// Load the V3D Raw file.
	ext := filepath.Ext(filename)
	switch ext {
	case ".raw", ".v3draw":
	default:
		return fmt.Errorf("Unknown extension '%s' when expected V3D Raw file", ext)
	}
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	unmarshaler := V3DRawMarshaler{}
	channels, err := unmarshaler.UnmarshalV3DRaw(file)
	if err != nil {
		return err
	}

	// Store the metadata
	d.NumChannels = len(channels)
	d.Properties.Values = make(dvid.DataValues, d.NumChannels)
	if d.NumChannels > 0 {
		d.ByteOrder = channels[0].ByteOrder()
		reply.Text = fmt.Sprintf("Loaded %s into data '%s': found %d channels\n",
			d.DataName(), filename, d.NumChannels)
		reply.Text += fmt.Sprintf(" %s", channels[0])
	} else {
		reply.Text = fmt.Sprintf("Found no channels in file %s\n", filename)
		return nil
	}
	for i, channel := range channels {
		d.Properties.Values[i] = channel.Voxels.Values()[0]
	}
	if err := service.SaveDataset(uuid); err != nil {
		return err
	}

	// PUT each channel of the file into the datastore using a separate data name.
	for _, channel := range channels {
		dvid.Fmt(dvid.Debug, "Processing channel %d... \n", channel.channelNum)
		err = voxels.PutVoxels(uuid, d, channel)
		if err != nil {
			return err
		}
	}

	// Create a RGB composite from the first 3 channels.  This is considered to be channel 0
	// or can be accessed with the base data name.
	dvid.Fmt(dvid.Debug, "Creating composite image from channels...\n")
	err = d.storeComposite(uuid, channels)
	if err != nil {
		return err
	}

	dvid.ElapsedTime(dvid.Debug, startTime, "RPC load local '%s' completed", filename)
	return nil
}

// Create a RGB interleaved volume.
func (d *Data) storeComposite(uuid dvid.UUID, channels []*Channel) error {
	// Setup the composite Channel
	geom := channels[0].Geometry
	pixels := int(geom.NumVoxels())
	stride := geom.Size().Value(0) * 4
	composite := &Channel{
		Voxels:     voxels.NewVoxels(geom, compositeValues, channels[0].Data(), stride, d.ByteOrder),
		channelNum: channels[0].channelNum,
	}

	// Get the min/max of each channel.
	numChannels := len(channels)
	if numChannels > 3 {
		numChannels = 3
	}
	var min, max [3]uint16
	min[0] = uint16(0xFFFF)
	min[1] = uint16(0xFFFF)
	min[2] = uint16(0xFFFF)
	for c := 0; c < numChannels; c++ {
		channel := channels[c]
		data := channel.Data()
		beg := 0
		for i := 0; i < pixels; i++ {
			value := d.ByteOrder.Uint16(data[beg : beg+2])
			if value < min[c] {
				min[c] = value
			}
			if value > max[c] {
				max[c] = value
			}
			beg += 2
		}
	}

	// Do second pass, normalizing each channel and storing it into the appropriate byte.
	compdata := composite.Voxels.Data()
	for c := 0; c < numChannels; c++ {
		channel := channels[c]
		window := int(max[c] - min[c])
		if window == 0 {
			window = 1
		}
		data := channel.Data()
		beg := 0
		begC := c // Channel 0 -> R, Channel 1 -> G, Channel 2 -> B
		for i := 0; i < pixels; i++ {
			value := d.ByteOrder.Uint16(data[beg : beg+2])
			normalized := 255 * int(value-min[c]) / window
			if normalized > 255 {
				normalized = 255
			}
			compdata[begC] = uint8(normalized)
			beg += 2
			begC += 4
		}
	}

	// Set the alpha channel to 255.
	alphaI := 3
	for i := 0; i < pixels; i++ {
		compdata[alphaI] = 255
		alphaI += 4
	}

	// Store the result
	return voxels.PutVoxels(uuid, d, composite)
}
