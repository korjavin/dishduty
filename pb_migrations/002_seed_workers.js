// @ts-check
/// <reference path="../pb_data/types.d.ts" />

migrate((db) => {
  const dao = new Dao(db);
  const collection = dao.findCollectionByNameOrId("workers");

  const workersToCreate = [
    { name: "keromag" },
    { name: "megatorg" },
    { name: "baby-ch" },
  ];

  for (const workerData of workersToCreate) {
    // Check if worker already exists to prevent errors if migration is run multiple times
    let existingWorker = null;
    try {
      existingWorker = dao.findFirstRecordByFilter(
        collection.id,
        `name = '${workerData.name}'`
      );
    } catch (e) {
      // PocketBase's findFirstRecordByFilter throws an error if no record is found.
      // If an error occurs, existingWorker remains null, and we proceed to create.
    }

    if (!existingWorker) {
      const record = new Record(collection, workerData);
      dao.saveRecord(record);
    }
  }
}, (db) => {
  const dao = new Dao(db);
  const collection = dao.findCollectionByNameOrId("workers");

  const workerNamesToDelete = ["keromag", "megatorg", "baby-ch"];

  for (const workerName of workerNamesToDelete) {
    try {
      const record = dao.findFirstRecordByFilter(
        collection.id,
        `name = '${workerName}'`
      );
      if (record) {
        dao.deleteRecord(record);
      }
    } catch (e) {
      // Record might not exist, which is fine for a down migration
      // console.warn(`Could not delete worker ${workerName}: ${e.message}`);
      // PocketBase migrations don't have console.warn, so we'll just ignore the error.
    }
  }
});