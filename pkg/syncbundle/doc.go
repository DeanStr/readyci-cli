// Package syncbundle defines the Ready CI workspace bundle protocol shared by
// upload clients, the API, and workers. Format version 3 binds recursive local
// submodule pins and reviewed fixture declarations into source identities;
// clients, admission, and workers must use this version together.
package syncbundle
