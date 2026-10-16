// Package model holds the definitions of SunSpec's information models: which points a model has, how many registers
// each takes, and how to read them. Package sunspec lays a device's registers out with them.
//
//	d := model.Lookup(103) // three-phase inverter
//	for _, p := range d.Group.Points {
//		fmt.Println(p.Name, p.Type, p.Units)
//	}
//
// Lookup knows every model SunSpec publishes at github.com/sunspec/models, generated into Go by tools/modelgen, so that
// nothing is parsed at run time. Parse reads one more, such as a vendor's, from the same JSON format.
//
// A model is a group of points, laid out one after the other, then of groups, each repeated as many times as its count
// says: fixed, held by a point read before, or as many as the rest of the model leaves room for. A point's value may
// scale with a scale factor: another point, of type sunssf, holding a power of ten.
//
// The definitions in models.go are SunSpec's, under the Apache License 2.0, in LICENSE. The rest of the package is
// under the module's MIT license.
package model
