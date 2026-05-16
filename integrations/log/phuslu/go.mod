// Separate module so the phuslu/log dependency does not enter the
// dependency closure of plain fastconf users.
module github.com/fastabc/fastconf/integrations/log/phuslu

go 1.26.2

require github.com/phuslu/log v1.0.124

replace github.com/fastabc/fastconf => ../../..
