package changelog

import "webtyp.com/model"

var ChangeModel = model.Definition{
    Name: "change_log",
    Fields: model.Fields{
        {Name: "version", Type: model.Int(), DB: &model.FieldDB{PK: true}},
        {Name: "table_name", Type: model.Text(), NotNull: true},
        {Name: "row_id", Type: model.Text(), NotNull: true},
        {Name: "op", Type: model.Text(), NotNull: true},
    },
}
