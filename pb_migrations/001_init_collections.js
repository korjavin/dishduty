// @ts-check
/// <reference path="../pb_data/types.d.ts" />

migrate((db) => {
  const dao = new Dao(db);

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
            collectionId: "_pb_users_auth_", // Placeholder, will be updated
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
          unique: true, 
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
            maxSize: 2000000,
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

  // Create workers collection first to get its ID
  const workersCollectionDef = collections.find(c => c.name === "workers");
  if (!workersCollectionDef) throw new Error("Workers collection definition not found");
  
  let actualWorkersCollectionId = "";
  try {
    const existingWorkersCollection = dao.findCollectionByNameOrId("workers");
    actualWorkersCollectionId = existingWorkersCollection.id;
  } catch (_) {
    // Collection does not exist, create it
    const savedWorkersCollection = dao.saveCollection(new Collection(workersCollectionDef));
    actualWorkersCollectionId = savedWorkersCollection.id;
  }


  for (const colDefData of collections) {
    if (colDefData.name === "workers") continue; // Already created or existed

    // Update relation collectionId for assignments and assignment_queue
    if (colDefData.name === "assignments" || colDefData.name === "assignment_queue") {
        const workerIdField = colDefData.schema.find(f => f.name === "worker_id");
        if (workerIdField && workerIdField.type === "relation") {
            workerIdField.options.collectionId = actualWorkersCollectionId;
        }
    }
    
    // The logic for default status and timestamp is generally handled by PocketBase
    // or application logic, not directly in schema definition this way for JS migrations.
    // Ensuring "assigned" is the first value in `status.options.values` is the typical approach.

    try {
      dao.findCollectionByNameOrId(colDefData.name);
      // Collection exists, do nothing or update if needed (not in scope for this task)
    } catch (_) {
      // Collection does not exist, create it
      dao.saveCollection(new Collection(colDefData));
    }
  }
}, async (db) => {
  const dao = new Dao(db);

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
      console.warn(`Could not delete collection ${name}: ${e.message}`);
    }
  }
});