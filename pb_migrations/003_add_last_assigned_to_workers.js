// pb_migrations/003_add_last_assigned_to_workers.js
migrate((db) => {
  const dao = new Dao(db);
  const collection = dao.findCollectionByNameOrId("workers");

  collection.schema.addField(new SchemaField({
    "system": false,
    "id": "last_assigned_date_field", // Unique ID for the field
    "name": "last_assigned_date",
    "type": "date",
    "required": false,
    "presentable": true,
    "unique": false,
    "options": {
      "min": "",
      "max": ""
    }
  }));

  return dao.saveCollection(collection);
}, (db) => {
  const dao = new Dao(db);
  const collection = dao.findCollectionByNameOrId("workers");

  // ID of the field to remove
  collection.schema.removeField("last_assigned_date_field");

  return dao.saveCollection(collection);
})