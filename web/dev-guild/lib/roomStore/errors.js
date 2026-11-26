// Error types shared by every room store adapter. Routes and the future
// domain-layer refactor branch on `code`, not on message text.
export class StoreError extends Error {
  constructor(message, code = "store_error") {
    super(message);
    this.name = new.target.name;
    this.code = code;
  }
}

export class StoreValidationError extends StoreError {
  constructor(message) {
    super(message, "validation");
  }
}

export class StoreConflictError extends StoreError {
  constructor(message = "room state changed since it was read") {
    super(message, "conflict");
  }
}

export class StoreSchemaError extends StoreError {
  constructor(message = "room store schema is missing; run scripts/migrate-guild-db.mjs") {
    super(message, "schema");
  }
}
