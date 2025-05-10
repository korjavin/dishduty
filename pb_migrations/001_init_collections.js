// @ts-check
/// <reference path="../pb_data/types.d.ts" />

/**
 * @param {import('pocketbase').Dao} dao
 */
module.exports.up = async (dao) => {
  const collections = [
    {
      name: "workers",
      type: "base",
      system: false,
      schema: [
        {
          name: "name",
          type: "text",
          required: true,
          unique: true,
          options: {
            min: null,
            max: null,
            pattern: "",
          },
        },
      ],
      indexes: ["CREATE UNIQUE INDEX idx_worker_name ON workers (name)"],
      listRule: null,
      viewRule: null,
      createRule: null,
      updateRule: null,
      deleteRule: null,
      options: {},
    },
    {
      name: "assignments",
      type: "base",
      system: false,
      schema: [
        {
          name: "worker_id",
          type: "relation",
          required: true,
          options: {
            collectionId: "_pb_users_auth_", // Placeholder, will be updated by collection name
            cascadeDelete: false,
            minSelect: null,
            maxSelect: 1,
            displayFields: [],
          },
        },
        {
          name: "date",
          type: "date",
          required: true,
          unique: true, // Unique for a given day (Pocketbase date field stores datetime)
          options: {
            min: "",
            max: "",
          },
        },
        {
          name: "status",
          type: "select",
          required: true,
          options: {
            maxSelect: 1,
            values: ["assigned", "done", "not_done"],
          },
        },
      ],
      indexes: ["CREATE UNIQUE INDEX idx_assignment_date ON assignments (date)"],
      listRule: null,
      viewRule: null,
      createRule: null,
      updateRule: null,
      deleteRule: null,
      options: {},
    },
    {
      name: "assignment_queue",
      type: "base",
      system: false,
      schema: [
        {
          name: "worker_id",
          type: "relation",
          required: true,
          options: {
            collectionId: "_pb_users_auth_", // Placeholder, will be updated
            cascadeDelete: false,
            minSelect: null,
            maxSelect: 1,
            displayFields: [],
          },
        },
        {
          name: "start_date",
          type: "date",
          required: true,
          options: {
            min: "",
            max: "",
          },
        },
        {
          name: "duration_days",
          type: "number",
          required: true,
          options: {
            min: 1,
            max: 7,
            noDecimal: true,
          },
        },
        {
          name: "order",
          type: "number",
          required: true,
          options: {
            min: null,
            max: null,
            noDecimal: true,
          },
        },
      ],
      indexes: [],
      listRule: null,
      viewRule: null,
      createRule: null,
      updateRule: null,
      deleteRule: null,
      options: {},
    },
    {
      name: "action_log",
      type: "base",
      system: false,
      schema: [
        {
          name: "timestamp",
          type: "date",
          required: true,
          options: {
            min: "",
            max: "",
          },
        },
        {
          name: "action_type",
          type: "select",
          required: true,
          options: {
            maxSelect: 1,
            values: [
              "assigned",
              "added_to_queue",
              "marked_not_done",
              "randomly_assigned",
              "queue_processed",
            ],
          },
        },
        {
          name: "details",
          type: "json",
          required: false,
          options: {
            maxSize: 2000000, // Default max size
          },
        },
      ],
      indexes: [],
      listRule: null,
      viewRule: null,
      createRule: null,
      updateRule: null,
      deleteRule: null,
      options: {},
    },
  ];

  const workersCollection = collections.find(c => c.name === "workers");
  if (!workersCollection) throw new Error("Workers collection definition not found");

  // Update relation fields to point to the actual workers collection ID
  for (const colDef of collections) {
    if (colDef.name === "assignments" || colDef.name === "assignment_queue") {
      const workerIdField = colDef.schema.find(f => f.name === "worker_id");
      if (workerIdField && workerIdField.type === "relation") {
        // This ID will be dynamically assigned by PocketBase when 'workers' is created.
        // In a real migration, you'd fetch the 'workers' collection first, get its ID,
        // then use it. For this static definition, we'll assume PocketBase handles
        // resolving this by name or we'd need a multi-step migration.
        // For now, we'll set it to the name, and PocketBase's JS migration runner
        // might resolve it or this would need to be adjusted.
        // A safer approach is to create workers first, then other collections.
        // Let's adjust the logic to create workers first.
      }
    }
  }

  // Create workers collection first to get its ID
  const workersCollectionDef = collections.find(c => c.name === "workers");
  if (!workersCollectionDef) throw new Error("Workers collection definition not found");
  
  const existingWorkersCollection = await dao.findCollectionByNameOrId("workers").catch(() => null);
  let actualWorkersCollectionId = existingWorkersCollection?.id;

  if (!existingWorkersCollection) {
    // Pass the definition directly
    const savedWorkersCollection = await dao.saveCollection(workersCollectionDef);
    actualWorkersCollectionId = savedWorkersCollection.id;
  }


  for (const colDef of collections) {
    if (colDef.name === "workers") continue; // Already created or existed

    // Update relation collectionId for assignments and assignment_queue
    if (colDef.name === "assignments" || colDef.name === "assignment_queue") {
        const workerIdField = colDef.schema.find(f => f.name === "worker_id");
        if (workerIdField && workerIdField.type === "relation") {
            workerIdField.options.collectionId = actualWorkersCollectionId;
        }
    }
    if (colDef.name === "assignments") {
        const statusField = colDef.schema.find(f => f.name === "status");
        if (statusField) {
            // @ts-ignore
            statusField.options.values.unshift("assigned"); // Ensure default is first
            // @ts-ignore
            statusField.default = "assigned"; // PocketBase JS SDK might not directly support default in schema definition this way
                                            // Default values for select are usually handled by setting the first value as default
                                            // or at application level. The schema implies the first value is default.
        }
    }
     if (colDef.name === "action_log") {
        const timestampField = colDef.schema.find(f => f.name === "timestamp");
        if (timestampField) {
            // Default to now is usually handled by PocketBase automatically for 'created' field
            // or application logic. For a regular date field, default 'now' isn't a direct schema option.
            // This will be set at record creation time.
        }
    }


    const existingCollection = await dao.findCollectionByNameOrId(colDef.name).catch(() => null);
    if (!existingCollection) {
      // Pass the definition directly
      // The logic for setting default 'status' should be part of the colDef if possible,
      // or handled by ensuring "assigned" is the first in the values array within colDef.
      // The existing colDef for assignments already has "assigned" as the first value in its schema.
      // So, no special handling for default status is needed here if the colDef is correct.
      await dao.saveCollection(colDef);
    }
  }
};

/**
 * @param {import('pocketbase').Dao} dao
 */
module.exports.down = async (dao) => {
  const collectionNames = [
    "action_log",
    "assignment_queue",
    "assignments",
    "workers",
  ]; // Delete in reverse order of creation dependency

  for (const name of collectionNames) {
    try {
      const collection = await dao.findCollectionByNameOrId(name);
      await dao.deleteCollection(collection);
    } catch (e) {
      //อาจจะ collection ไม่มี
      console.warn(`Could not delete collection ${name}: ${e.message}`);
    }
  }
};