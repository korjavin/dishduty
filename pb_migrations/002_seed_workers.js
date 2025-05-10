// @ts-check
/// <reference path="../pb_data/types.d.ts" />

/**
 * @param {import('pocketbase').Dao} dao
 */
module.exports.up = async (dao) => {
  const collection = await dao.findCollectionByNameOrId("workers");

  const workersToCreate = [
    { name: "keromag" },
    { name: "megatorg" },
    { name: "baby-ch" },
  ];

  for (const workerData of workersToCreate) {
    // Check if worker already exists to prevent errors if migration is run multiple times
    const existingWorker = await dao.findFirstRecordByFilter(
      collection.id,
      `name = '${workerData.name}'`
    ).catch(() => null);

    if (!existingWorker) {
      const record = new Record(collection, workerData);
      await dao.saveRecord(record);
    }
  }
};

/**
 * @param {import('pocketbase').Dao} dao
 */
module.exports.down = async (dao) => {
  const collection = await dao.findCollectionByNameOrId("workers");

  const workerNamesToDelete = ["keromag", "megatorg", "baby-ch"];

  for (const workerName of workerNamesToDelete) {
    try {
      const record = await dao.findFirstRecordByFilter(
        collection.id,
        `name = '${workerName}'`
      );
      if (record) {
        await dao.deleteRecord(record);
      }
    } catch (e) {
      // Record might not exist, which is fine for a down migration
      console.warn(`Could not delete worker ${workerName}: ${e.message}`);
    }
  }
};